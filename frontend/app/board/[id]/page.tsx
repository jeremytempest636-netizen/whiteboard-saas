"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { ConfirmClear, ConnectionBadge, CopyLinkButton, PresenceStack } from "@/components/BoardChrome";
import { NameForm } from "@/components/NameForm";
import { Tray } from "@/components/Tray";
import { WhiteboardCanvas, type WhiteboardCanvasHandle } from "@/components/WhiteboardCanvas";
import { useBoard } from "@/hooks/useBoard";
import { useSession } from "@/hooks/useSession";
import { api, ApiError, type Board } from "@/lib/api";
import { exportBoardPng } from "@/lib/exportPng";
import type { Tool } from "@/lib/types";

type Meta = { state: "loading" } | { state: "ok"; board: Board } | { state: "missing" } | { state: "error"; message: string } | { state: "auth" };

const SHORTCUTS: Record<string, Tool> = { v: "select", p: "pen", r: "rect", o: "ellipse", a: "arrow", t: "text", e: "eraser", h: "hand" };

export default function BoardPage() {
  const params = useParams<{ id: string }>();
  const boardId = (params.id ?? "").toLowerCase();
  const { ready, session, signIn, signOut } = useSession();
  const [meta, setMeta] = useState<Meta>({ state: "loading" });

  // Confirm the board exists (and the session is valid) before opening a socket.
  useEffect(() => {
    if (!ready) return;
    if (!session) {
      setMeta({ state: "auth" });
      return;
    }
    let cancelled = false;
    setMeta({ state: "loading" });
    api
      .getBoard(session.token, boardId)
      .then((board) => !cancelled && setMeta({ state: "ok", board }))
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setMeta({ state: "missing" });
        else if (err instanceof ApiError && err.status === 401) {
          signOut();
          setMeta({ state: "auth" });
        } else setMeta({ state: "error", message: err instanceof Error ? err.message : "Something went wrong." });
      });
    return () => {
      cancelled = true;
    };
  }, [ready, session, boardId, signOut]);

  useEffect(() => {
    document.title = meta.state === "ok" ? `${meta.board.title} · Whiteboard` : "Whiteboard";
  }, [meta]);

  if (meta.state === "ok" && session) {
    return <Room board={meta.board} token={session.token} />;
  }

  return (
    <main className="grid min-h-screen place-items-center p-6">
      {meta.state === "auth" && (
        <div className="w-full max-w-sm rounded-xl bg-board p-6 ring-1 ring-ink/15">
          <h1 className="text-2xl font-semibold tracking-tight">Join this board</h1>
          <p className="mt-1 mb-5 text-ink-soft">Pick a name so the others can see who is drawing.</p>
          <NameForm
            submitLabel="Join board"
            onSubmit={async (name) => {
              await signIn(name);
            }}
          />
        </div>
      )}
      {meta.state === "loading" && <p className="text-ink-soft">Opening board…</p>}
      {meta.state === "missing" && (
        <div className="max-w-sm">
          <h1 className="text-2xl font-semibold">No board with that code</h1>
          <p className="mt-2 text-ink-soft">Check the link you were sent, or start a fresh board.</p>
          <Link href="/" className="mt-5 inline-flex h-11 items-center rounded-lg bg-ink px-5 font-medium text-board">
            Start a board
          </Link>
        </div>
      )}
      {meta.state === "error" && (
        <div className="max-w-sm">
          <h1 className="text-2xl font-semibold">Couldn't open the board</h1>
          <p className="mt-2 text-ink-soft">{meta.message}</p>
          <button type="button" onClick={() => window.location.reload()} className="mt-5 h-11 rounded-lg bg-ink px-5 font-medium text-board">
            Try again
          </button>
        </div>
      )}
    </main>
  );
}

function Room({ board, token }: { board: Board; token: string }) {
  const b = useBoard(board.id, token);
  const canvas = useRef<WhiteboardCanvasHandle>(null);

  const [tool, setTool] = useState<Tool>("pen");
  const [color, setColor] = useState("#1b1f23");
  const [width, setWidth] = useState(4);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);
  const [confirmClear, setConfirmClear] = useState(false);

  const { commit, getElement } = b;

  const deleteSelected = useCallback(() => {
    if (!selectedId) return;
    const el = getElement(selectedId);
    if (el) commit([{ id: el.id, before: el, after: undefined }]);
    setSelectedId(null);
  }, [selectedId, getElement, commit]);

  // Choosing a colour or width also restyles the selected shape.
  const restyle = (patch: { color?: string; width?: number }) => {
    const el = selectedId ? getElement(selectedId) : undefined;
    if (!el || tool !== "select") return;
    const next = { ...el, ...(patch.color ? { color: patch.color } : {}), ...(patch.width && "width" in el ? { width: patch.width } : {}) };
    commit([{ id: el.id, before: el, after: next }]);
  };

  const pickColor = (c: string) => {
    setColor(c);
    if (tool === "select") restyle({ color: c });
    else if (tool === "eraser" || tool === "hand") setTool("pen");
  };
  const pickWidth = (w: number) => {
    setWidth(w);
    if (tool === "select") restyle({ width: w });
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target;
      if (t instanceof HTMLElement && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      const mod = e.ctrlKey || e.metaKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        if (e.shiftKey) b.redo();
        else b.undo();
        return;
      }
      if (mod && e.key.toLowerCase() === "y") {
        e.preventDefault();
        b.redo();
        return;
      }
      if (mod || e.altKey) return;
      if (e.key === "Delete" || e.key === "Backspace") {
        e.preventDefault();
        deleteSelected();
        return;
      }
      if (e.key === "Escape") {
        setSelectedId(null);
        return;
      }
      const next = SHORTCUTS[e.key.toLowerCase()];
      if (next) {
        setTool(next);
        if (next !== "select") setSelectedId(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [b, deleteSelected]);

  const exportPng = () => {
    const url = exportBoardPng(b.elements);
    if (!url) return;
    const a = document.createElement("a");
    a.href = url;
    a.download = `${board.title.replace(/[^\w\-]+/g, "-").replace(/^-+|-+$/g, "") || "board"}.png`;
    a.click();
  };

  const showOfflineBanner = b.status === "offline" || b.status === "reconnecting";

  return (
    <div className="flex h-dvh flex-col px-2 pb-2 sm:px-3 sm:pb-3">
      <header className="flex h-14 shrink-0 items-center justify-between gap-3 px-1">
        <div className="flex min-w-0 items-center gap-3">
          <Link href="/" className="shrink-0 text-lg font-semibold tracking-tight" aria-label="Whiteboard home">
            <span className="hidden sm:inline">Whiteboard</span>
            <span className="sm:hidden">WB</span>
          </Link>
          <span className="h-5 w-px bg-ink/25" aria-hidden="true" />
          <h1 className="truncate font-medium">{board.title}</h1>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <PresenceStack me={b.me} peers={b.peers} />
          <ConnectionBadge status={b.status} onRetry={b.reconnect} />
          <CopyLinkButton />
        </div>
      </header>

      <div className="board-frame min-h-0 flex-1">
        <div className="board-surface h-full">
          <WhiteboardCanvas
            ref={canvas}
            elements={b.elements}
            getElement={b.getElement}
            tool={tool}
            color={color}
            strokeWidth={width}
            selectedId={selectedId}
            onSelect={setSelectedId}
            onLive={b.live}
            onCommit={b.commit}
            beginGroup={b.beginGroup}
            endGroup={b.endGroup}
            peers={b.peers}
            cursors={b.cursors}
            meId={b.me?.id}
            onCursor={b.sendCursor}
            onScaleChange={setZoom}
            onToolDone={() => undefined}
            fitSignal={b.loadCount}
          />

          {(b.fatal || showOfflineBanner || b.notice) && (
            <div className="pointer-events-none absolute inset-x-0 top-3 z-10 flex justify-center px-3">
              <p role="status" className="pill pointer-events-auto max-w-lg px-4 py-2 text-center text-sm">
                {b.fatal ??
                  b.notice ??
                  (b.status === "offline"
                    ? "You're offline. Keep drawing: your changes are saved here and sync when you reconnect."
                    : "Connection lost. Reconnecting…")}
              </p>
            </div>
          )}

          <div className="absolute bottom-4 left-1/2 z-10 -translate-x-1/2">
            <Tray
              tool={tool}
              onTool={(t) => {
                setTool(t);
                if (t !== "select") setSelectedId(null);
              }}
              color={color}
              onColor={pickColor}
              width={width}
              onWidth={pickWidth}
              canUndo={b.canUndo}
              canRedo={b.canRedo}
              onUndo={b.undo}
              onRedo={b.redo}
              onClear={() => setConfirmClear(true)}
              onExport={exportPng}
              zoom={zoom}
              onZoomIn={() => canvas.current?.zoomBy(1.25)}
              onZoomOut={() => canvas.current?.zoomBy(0.8)}
              onFit={() => canvas.current?.fit()}
            />
          </div>

          {confirmClear && (
            <ConfirmClear
              onCancel={() => setConfirmClear(false)}
              onConfirm={() => {
                b.clearBoard();
                setSelectedId(null);
                setConfirmClear(false);
              }}
            />
          )}
        </div>
      </div>
    </div>
  );
}
