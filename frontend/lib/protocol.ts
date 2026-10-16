import type { Peer, WireElement } from "./types";

/** client -> server */
export type ClientMessage =
  | { t: "op"; els: WireElement[] }
  | { t: "clear"; ts: number }
  | { t: "cursor"; x: number; y: number }
  | { t: "ping" };

/** server -> client */
export type ServerMessage =
  | {
      t: "init";
      clientId: string;
      you: Peer;
      els: WireElement[];
      peers: Peer[];
      clearedAt: number;
      now: number;
    }
  | { t: "op"; from: string; els: WireElement[] }
  | { t: "clear"; from: string; ts: number }
  | { t: "cursor"; from: string; x: number; y: number }
  | { t: "join"; peer: Peer }
  | { t: "leave"; id: string }
  | { t: "pong" }
  | { t: "error"; code: string; message: string };

export function parseServerMessage(raw: string): ServerMessage | null {
  try {
    const m = JSON.parse(raw) as { t?: unknown };
    return typeof m.t === "string" ? (m as ServerMessage) : null;
  } catch {
    return null;
  }
}
