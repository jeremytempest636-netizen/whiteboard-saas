import test from "node:test";
import assert from "node:assert/strict";
import { boundsOf, hitTest, pick, unionBounds } from "./geometry.ts";
import type { BoardElement } from "./types.ts";

const base = { ts: 1, z: 1, color: "#000" };
const rect: BoardElement = { ...base, id: "r", type: "rect", x: 0, y: 0, w: 100, h: 50, width: 4 };
const ellipse: BoardElement = { ...base, id: "e", type: "ellipse", x: 0, y: 0, w: 100, h: 60, width: 4 };
const line: BoardElement = { ...base, id: "l", type: "pen", x: 10, y: 10, points: [0, 0, 100, 0], width: 4 };

test("rectangles are hit on the outline, not inside", () => {
  assert.equal(hitTest(rect, 0, 25, 3), true);
  assert.equal(hitTest(rect, 50, 25, 3), false);
  assert.equal(hitTest(rect, 200, 25, 3), false);
});

test("ellipse outline hit", () => {
  assert.equal(hitTest(ellipse, 0, 30, 3), true);
  assert.equal(hitTest(ellipse, 50, 30, 3), false);
  assert.equal(hitTest(ellipse, 50, 0, 3), true);
});

test("polyline hit uses distance to the segment", () => {
  assert.equal(hitTest(line, 60, 12, 2), true);
  assert.equal(hitTest(line, 60, 30, 2), false);
  assert.equal(hitTest(line, 200, 10, 2), false);
});

test("pick returns the topmost match", () => {
  const top: BoardElement = { ...rect, id: "top" };
  assert.equal(pick([rect, top], 0, 25, 3)?.id, "top");
});

test("bounds and union", () => {
  assert.deepEqual(boundsOf(line), { x: 10, y: 10, w: 100, h: 0 });
  assert.deepEqual(unionBounds([boundsOf(rect), boundsOf(line)]), { x: 0, y: 0, w: 110, h: 50 });
  assert.equal(unionBounds([]), null);
});
