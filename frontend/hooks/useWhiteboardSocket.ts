"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { WS_URL } from "@/lib/config";
import { parseServerMessage, type ClientMessage, type ServerMessage } from "@/lib/protocol";
import type { ConnectionStatus } from "@/lib/types";

interface Options {
  boardId: string;
  /** Session token; the socket stays closed while this is null. */
  token: string | null;
  /** Called for every parsed server message. Always the latest closure is used. */
  onMessage: (msg: ServerMessage) => void;
  /** Called after every successful (re)connection, once `init` was handled. */
  onConnected?: (isReconnect: boolean) => void;
}

interface SocketApi {
  status: ConnectionStatus;
  /** Sends a message; returns false when the socket is not ready. */
  send: (msg: ClientMessage) => boolean;
  /** Skips the backoff wait and reconnects now. */
  reconnect: () => void;
  /** Set when the server refused the board for good (for example it is full). */
  fatal: string | null;
}

const PING_EVERY_MS = 15_000;
const STALE_AFTER_MS = 40_000;
const BASE_DELAY_MS = 500;
const MAX_DELAY_MS = 10_000;
const OFFLINE_AFTER_FAILURES = 4;

/**
 * Keeps one WebSocket to the board alive.
 *
 *  - reconnects with exponential backoff and jitter;
 *  - detects dead connections with an application heartbeat (browsers do not
 *    expose WebSocket ping frames), because a half-open TCP connection can look
 *    healthy for minutes;
 *  - reconnects immediately when the network or the tab comes back.
 */
export function useWhiteboardSocket({ boardId, token, onMessage, onConnected }: Options): SocketApi {
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [fatal, setFatal] = useState<string | null>(null);

  const socketRef = useRef<WebSocket | null>(null);
  const liveRef = useRef(false);
  const onMessageRef = useRef(onMessage);
  const onConnectedRef = useRef(onConnected);
  const connectRef = useRef<() => void>(() => {});
  onMessageRef.current = onMessage;
  onConnectedRef.current = onConnected;

  useEffect(() => {
    if (!token) return;

    let disposed = false;
    let attempt = 0;
    let failures = 0;
    let everConnected = false;
    let lastMessageAt = Date.now();
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let heartbeat: ReturnType<typeof setInterval> | undefined;

    const clearTimers = () => {
      clearTimeout(retryTimer);
      clearInterval(heartbeat);
    };

    const scheduleRetry = () => {
      if (disposed) return;
      const ceiling = Math.min(MAX_DELAY_MS, BASE_DELAY_MS * 2 ** attempt);
      const delay = ceiling * (0.5 + Math.random() / 2);
      attempt++;
      clearTimeout(retryTimer);
      retryTimer = setTimeout(connect, delay);
    };

    function connect() {
      if (disposed) return;
      clearTimeout(retryTimer);
      const prev = socketRef.current;
      if (prev && prev.readyState <= WebSocket.OPEN) prev.close();

      const url = `${WS_URL}/ws/boards/${encodeURIComponent(boardId)}?token=${encodeURIComponent(token as string)}`;
      const ws = new WebSocket(url);
      socketRef.current = ws;
      liveRef.current = false;
      let gotInit = false;
      setStatus(everConnected ? "reconnecting" : "connecting");

      ws.onopen = () => {
        lastMessageAt = Date.now();
        clearInterval(heartbeat);
        heartbeat = setInterval(() => {
          if (ws.readyState !== WebSocket.OPEN) return;
          if (Date.now() - lastMessageAt > STALE_AFTER_MS) {
            ws.close(); // triggers onclose, which schedules the retry
            return;
          }
          ws.send(JSON.stringify({ t: "ping" }));
        }, PING_EVERY_MS);
      };

      ws.onmessage = (ev) => {
        lastMessageAt = Date.now();
        const msg = typeof ev.data === "string" ? parseServerMessage(ev.data) : null;
        if (!msg) return;
        if (msg.t === "init") {
          const isReconnect = everConnected;
          gotInit = true;
          everConnected = true;
          attempt = 0;
          failures = 0;
          liveRef.current = true;
          onMessageRef.current(msg);
          setStatus("live");
          onConnectedRef.current?.(isReconnect);
          return;
        }
        if (msg.t !== "pong") onMessageRef.current(msg);
      };

      ws.onclose = (ev) => {
        if (socketRef.current !== ws) return; // superseded by a newer socket
        liveRef.current = false;
        clearInterval(heartbeat);
        if (disposed) return;

        if (ev.code === 1008 && ev.reason === "board is full") {
          setFatal("This board has reached its limit of people. Try again in a moment.");
          setStatus("offline");
          return;
        }
        if (!gotInit) failures++;
        setStatus(failures >= OFFLINE_AFTER_FAILURES || !navigator.onLine ? "offline" : "reconnecting");
        scheduleRetry();
      };

      // onerror is always followed by onclose, which owns the retry logic.
      ws.onerror = () => {};
    }

    connectRef.current = () => {
      attempt = 0;
      connect();
    };

    const onOnline = () => connectRef.current();
    const onOffline = () => setStatus("offline");
    const onVisible = () => {
      if (document.visibilityState === "visible" && !liveRef.current) connectRef.current();
    };
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    document.addEventListener("visibilitychange", onVisible);

    connect();

    return () => {
      disposed = true;
      clearTimers();
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
      document.removeEventListener("visibilitychange", onVisible);
      const ws = socketRef.current;
      socketRef.current = null;
      liveRef.current = false;
      if (ws && ws.readyState <= WebSocket.OPEN) ws.close(1000, "leaving");
    };
  }, [boardId, token]);

  const send = useCallback((msg: ClientMessage): boolean => {
    const ws = socketRef.current;
    if (!ws || !liveRef.current || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(msg));
    return true;
  }, []);

  const reconnect = useCallback(() => connectRef.current(), []);

  return { status, send, reconnect, fatal };
}
