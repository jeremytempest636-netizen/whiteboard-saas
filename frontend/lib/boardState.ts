/**
 * Pure replication rules for the client copy of a board. They mirror the
 * server (backend/internal/canvas): last writer wins per element, deletions are
 * tombstones, and "clear" raises a watermark. Keeping them pure makes them
 * unit-testable without React or a socket.
 */
import type { BoardElement, Change, WireElement } from "./types";

export interface Doc {
  els: Map<string, WireElement>;
  clearedAt: number;
}

export function newDoc(): Doc {
  return { els: new Map(), clearedAt: 0 };
}

export function isTombstone(el: WireElement): el is Extract<WireElement, { deleted: true }> {
  return el.deleted === true;
}

/** Merges one element. Returns true when the document changed. */
export function mergeElement(doc: Doc, el: WireElement): boolean {
  if (el.ts <= doc.clearedAt) return false;
  const cur = doc.els.get(el.id);
  if (cur && cur.ts >= el.ts) return false;
  doc.els.set(el.id, el);
  return true;
}

/** Merges a batch. Returns true when anything changed. */
export function mergeElements(doc: Doc, els: readonly WireElement[]): boolean {
  let changed = false;
  for (const el of els) changed = mergeElement(doc, el) || changed;
  return changed;
}

/** Applies a clear. Elements newer than the clear survive. */
export function applyClear(doc: Doc, ts: number): boolean {
  if (ts <= doc.clearedAt) return false;
  doc.clearedAt = ts;
  for (const [id, el] of doc.els) if (el.ts <= ts) doc.els.delete(id);
  return true;
}

/** Replaces the document with server state (used on every (re)connect). */
export function resetDoc(doc: Doc, els: readonly WireElement[], clearedAt: number): void {
  doc.els.clear();
  doc.clearedAt = clearedAt;
  mergeElements(doc, els);
}

/** Visible elements in stacking order (oldest at the bottom). */
export function visibleElements(doc: Doc): BoardElement[] {
  const out: BoardElement[] = [];
  for (const el of doc.els.values()) if (!isTombstone(el)) out.push(el);
  out.sort((a, b) => a.z - b.z || (a.id < b.id ? -1 : 1));
  return out;
}

/**
 * Turns a history entry into the elements to write when undoing it (or, with
 * `redo`, re-doing it). Every write gets a fresh timestamp so it beats the
 * state it reverts.
 */
export function revertChanges(
  changes: readonly Change[],
  direction: "undo" | "redo",
  stamp: (id: string) => number,
): WireElement[] {
  const out: WireElement[] = [];
  for (const c of changes) {
    const target = direction === "undo" ? c.before : c.after;
    const ts = stamp(c.id);
    if (target) out.push({ ...target, deleted: undefined, ts });
    else out.push({ id: c.id, ts, deleted: true });
  }
  return out;
}

/** Generates timestamps that never repeat or go backwards for one id. */
export function makeStamper(getPrev: (id: string) => number, offset: () => number) {
  let last = 0;
  return (id: string): number => {
    const now = Date.now() + offset();
    const ts = Math.max(now, getPrev(id) + 1, last + 1);
    last = ts;
    return ts;
  };
}
