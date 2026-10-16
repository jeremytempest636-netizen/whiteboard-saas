"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  applyClear,
  isTombstone,
  makeStamper,
  mergeElement,
  mergeElements,
  newDoc,
  resetDoc,
  revertChanges,
  visibleElements,
} from "@/lib/boardState";
import { createCursorStore } from "@/lib/cursorStore";
import type { ClientMessage, ServerMessage } from "@/lib/protocol";
import type { BoardElement, Change, ConnectionStatus, Peer, WireElement } from "@/lib/types";
import { useWhiteboardSocket } from "./useWhiteboardSocket";

const LIVE_FLUSH_MS = 60;
const MAX_HISTORY = 100;
const OP_BATCH = 200; // the server accepts up to 500 elements per op

export interface BoardApi {
  status: ConnectionStatus;
  fatal: string | null;
  reconnect: () => void;
  elements: BoardElement[];
  getElement: (id: string) => BoardElement | undefined;
  me: Peer | null;
  peers: Peer[];
  cursors: ReturnType<typeof createCursorStore>;
  /** Increases each time the server sent a full board (join or reconnect). */
  loadCount: number;
  /** Shows a shape that is still being drawn and streams it to others. */
  live: (el: BoardElement) => void;
  /** Finalises edits; they become one undo step (or part of the open group). */
  commit: (changes: Change[]) => void;
  beginGroup: () => void;
  endGroup: () => void;
  clearBoard: () => void;
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
  sendCursor: (x: number, y: number) => void;
  notice: string | null;
}

/**
 * Owns the client copy of a board: applies remote messages, writes local edits,
 * keeps undo history, and re-sends this user's edits after a reconnect so a
 * dropped connection never loses work.
 */
export function useBoard(boardId: string, token: string | null): BoardApi {
  const doc = useRef(newDoc()).current;
  const cursors = useMemo(() => createCursorStore(), []);
  const clockOffset = useRef(0);
  const stamp = useMemo(
    () => makeStamper((id) => doc.els.get(id)?.ts ?? 0, () => clockOffset.current),
    [doc],
  );

  const [elements, setElements] = useState<BoardElement[]>([]);
  const [peers, setPeers] = useState<Peer[]>([]);
  const [me, setMe] = useState<Peer | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [histVersion, setHistVersion] = useState(0);
  const [loadCount, setLoadCount] = useState(0);
  const initPending = useRef(false);

  // Latest own write per element id: replayed after a reconnect (idempotent).
  const mine = useRef(new Map<string, WireElement>());
  const myClear = useRef(0);
  const liveBuf = useRef(new Map<string, WireElement>());
  const liveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const undoStack = useRef<Change[][]>([]);
  const redoStack = useRef<Change[][]>([]);
  const group = useRef<Change[] | null>(null);

  const renderFrame = useRef(0);
  const render = useCallback(() => {
    if (renderFrame.current) return;
    renderFrame.current = requestAnimationFrame(() => {
      renderFrame.current = 0;
      setElements(visibleElements(doc));
      if (initPending.current) {
        // Announced in the same frame as the elements, so listeners can rely on both.
        initPending.current = false;
        setLoadCount((n) => n + 1);
      }
    });
  }, [doc]);

  const noticeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const showNotice = useCallback((text: string) => {
    setNotice(text);
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), 4000);
  }, []);

  const handleMessage = useCallback(
    (msg: ServerMessage) => {
      switch (msg.t) {
        case "init":
          clockOffset.current = msg.now - Date.now();
          resetDoc(doc, msg.els, msg.clearedAt);
          cursors.clear();
          setPeers(msg.peers);
          setMe(msg.you);
          initPending.current = true;
          render();
          break;
        case "op":
          if (mergeElements(doc, msg.els)) render();
          break;
        case "clear":
          if (applyClear(doc, msg.ts)) render();
          break;
        case "cursor":
          cursors.set(msg.from, msg.x, msg.y);
          break;
        case "join":
          setPeers((p) => (p.some((x) => x.id === msg.peer.id) ? p : [...p, msg.peer]));
          break;
        case "leave":
          setPeers((p) => p.filter((x) => x.id !== msg.id));
          cursors.remove(msg.id);
          break;
        case "error":
          showNotice(
            msg.code === "rate_limited"
              ? "You're drawing faster than the board can sync."
              : "The board rejected one of your changes.",
          );
          break;
      }
    },
    [doc, cursors, render, showNotice],
  );

  const socketSend = useRef<(m: ClientMessage) => boolean>(() => false);

  const replayOwnWork = useCallback(() => {
    // After a reconnect the server state replaced ours. Re-apply and re-send
    // everything this user wrote; last-writer-wins makes that harmless for
    // elements somebody else changed in the meantime.
    if (myClear.current > 0) {
      applyClear(doc, myClear.current);
      socketSend.current({ t: "clear", ts: myClear.current });
    }
    const batch: WireElement[] = [];
    const flush = () => {
      if (batch.length) socketSend.current({ t: "op", els: batch.splice(0, batch.length) });
    };
    for (const el of mine.current.values()) {
      if (el.ts <= myClear.current) continue;
      mergeElement(doc, el);
      batch.push(el);
      if (batch.length >= OP_BATCH) flush();
    }
    flush();
    render();
  }, [doc, render]);

  const { status, send, reconnect, fatal } = useWhiteboardSocket({
    boardId,
    token,
    onMessage: handleMessage,
    onConnected: (isReconnect) => {
      if (isReconnect) replayOwnWork();
    },
  });
  socketSend.current = send;

  // --- writing ---------------------------------------------------------------

  const flushLive = useCallback(() => {
    clearTimeout(liveTimer.current);
    liveTimer.current = undefined;
    if (liveBuf.current.size === 0) return;
    const els = [...liveBuf.current.values()];
    liveBuf.current.clear();
    send({ t: "op", els });
  }, [send]);

  const write = useCallback(
    (els: WireElement[]) => {
      for (const el of els) {
        mine.current.set(el.id, el);
        liveBuf.current.delete(el.id);
      }
      mergeElements(doc, els);
      render();
      send({ t: "op", els });
    },
    [doc, render, send],
  );

  const live = useCallback(
    (el: BoardElement) => {
      const stamped = { ...el, ts: stamp(el.id) };
      mine.current.set(el.id, stamped);
      mergeElement(doc, stamped);
      render();
      liveBuf.current.set(el.id, stamped);
      liveTimer.current ??= setTimeout(flushLive, LIVE_FLUSH_MS);
    },
    [doc, render, stamp, flushLive],
  );

  const pushHistory = useCallback((changes: Change[]) => {
    if (changes.length === 0) return;
    undoStack.current.push(changes);
    if (undoStack.current.length > MAX_HISTORY) undoStack.current.shift();
    redoStack.current = [];
    setHistVersion((v) => v + 1);
  }, []);

  const commit = useCallback(
    (changes: Change[]) => {
      if (changes.length === 0) return;
      const recorded: Change[] = [];
      const wire: WireElement[] = [];
      for (const c of changes) {
        const ts = stamp(c.id);
        if (c.after) {
          const after = { ...c.after, ts };
          wire.push(after);
          recorded.push({ id: c.id, before: c.before, after });
        } else {
          wire.push({ id: c.id, ts, deleted: true });
          recorded.push({ id: c.id, before: c.before, after: undefined });
        }
      }
      write(wire);
      // A shape created and discarded in one gesture leaves nothing to undo.
      const undoable = recorded.filter((c) => c.before || c.after);
      if (group.current) group.current.push(...undoable);
      else pushHistory(undoable);
    },
    [stamp, write, pushHistory],
  );

  const beginGroup = useCallback(() => {
    group.current = [];
  }, []);

  const endGroup = useCallback(() => {
    const g = group.current;
    group.current = null;
    if (g && g.length) pushHistory(g);
  }, [pushHistory]);

  const applyHistory = useCallback(
    (direction: "undo" | "redo") => {
      const from = direction === "undo" ? undoStack : redoStack;
      const to = direction === "undo" ? redoStack : undoStack;
      const entry = from.current.pop();
      if (!entry) return;
      write(revertChanges(entry, direction, stamp));
      to.current.push(entry);
      setHistVersion((v) => v + 1);
    },
    [stamp, write],
  );
  const undo = useCallback(() => applyHistory("undo"), [applyHistory]);
  const redo = useCallback(() => applyHistory("redo"), [applyHistory]);

  const clearBoard = useCallback(() => {
    const ts = stamp("__clear__");
    myClear.current = ts;
    mine.current.clear();
    undoStack.current = [];
    redoStack.current = [];
    setHistVersion((v) => v + 1);
    applyClear(doc, ts);
    render();
    send({ t: "clear", ts });
  }, [doc, render, send, stamp]);

  const sendCursor = useCallback((x: number, y: number) => void send({ t: "cursor", x, y }), [send]);

  const getElement = useCallback(
    (id: string): BoardElement | undefined => {
      const el = doc.els.get(id);
      return el && !isTombstone(el) ? el : undefined;
    },
    [doc],
  );

  useEffect(
    () => () => {
      clearTimeout(liveTimer.current);
      clearTimeout(noticeTimer.current);
      cancelAnimationFrame(renderFrame.current);
    },
    [],
  );

  void histVersion; // re-render trigger for canUndo / canRedo below
  return {
    status,
    fatal,
    reconnect,
    loadCount,
    elements,
    getElement,
    me,
    peers,
    cursors,
    live,
    commit,
    beginGroup,
    endGroup,
    clearBoard,
    undo,
    redo,
    canUndo: undoStack.current.length > 0,
    canRedo: redoStack.current.length > 0,
    sendCursor,
    notice,
  };
}
