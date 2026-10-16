import test from "node:test";
import assert from "node:assert/strict";
import {
  applyClear,
  makeStamper,
  mergeElement,
  newDoc,
  resetDoc,
  revertChanges,
  visibleElements,
} from "./boardState.ts";

const rect = (id: string, ts: number, z = ts) =>
  ({ id, ts, z, type: "rect", x: 0, y: 0, w: 10, h: 10, width: 3, color: "#000" }) as const;

test("newer write wins, older is ignored", () => {
  const d = newDoc();
  assert.equal(mergeElement(d, rect("a", 100)), true);
  assert.equal(mergeElement(d, rect("a", 90)), false);
  assert.equal(mergeElement(d, rect("a", 110)), true);
  assert.equal(d.els.get("a")?.ts, 110);
});

test("tombstone blocks a stale resurrection and hides the element", () => {
  const d = newDoc();
  mergeElement(d, rect("a", 100));
  mergeElement(d, { id: "a", ts: 200, deleted: true });
  assert.equal(mergeElement(d, rect("a", 150)), false);
  assert.equal(visibleElements(d).length, 0);
});

test("clear keeps only elements newer than the watermark", () => {
  const d = newDoc();
  mergeElement(d, rect("old", 100));
  mergeElement(d, rect("new", 300));
  assert.equal(applyClear(d, 200), true);
  assert.deepEqual(visibleElements(d).map((e) => e.id), ["new"]);
  assert.equal(mergeElement(d, rect("late", 150)), false);
  assert.equal(applyClear(d, 150), false);
});

test("visible elements are ordered by creation time", () => {
  const d = newDoc();
  mergeElement(d, rect("b", 500, 2));
  mergeElement(d, rect("a", 100, 1));
  assert.deepEqual(visibleElements(d).map((e) => e.id), ["a", "b"]);
});

test("reset replaces everything with server state", () => {
  const d = newDoc();
  mergeElement(d, rect("local", 100));
  resetDoc(d, [rect("server", 50)], 10);
  assert.deepEqual(visibleElements(d).map((e) => e.id), ["server"]);
  assert.equal(d.clearedAt, 10);
});

test("undo restores the previous version with a newer timestamp", () => {
  const before = rect("a", 100);
  const after = { ...rect("a", 200), x: 50 };
  const stamp = makeStamper(() => 200, () => 0);
  const [undo] = revertChanges([{ id: "a", before, after }], "undo", stamp);
  assert.ok(undo && !("deleted" in undo && undo.deleted));
  assert.equal((undo as typeof before).x, 0);
  assert.ok(undo!.ts > 200);

  const [del] = revertChanges([{ id: "a", before: undefined, after }], "undo", stamp);
  assert.equal(del && "deleted" in del && del.deleted, true);
});

test("stamper is strictly increasing and respects the server offset", () => {
  const s = makeStamper(() => 0, () => 5000);
  const a = s("x");
  const b = s("x");
  assert.ok(b > a);
  assert.ok(a >= Date.now() + 4000);
});
