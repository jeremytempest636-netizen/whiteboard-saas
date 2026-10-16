/**
 * Geometry helpers shared by selection, erasing and export. Everything is in
 * board coordinates; `tol` is a hit tolerance in board units (callers divide a
 * screen-space tolerance by the zoom level).
 */
import type { BoardElement, TextElement } from "./types";

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export const TEXT_LINE_HEIGHT = 1.2;
export const TEXT_FONT = '"Caveat Brush", "Segoe Print", cursive';

type Measurer = (text: string, size: number) => { w: number; h: number };

const approximate: Measurer = (text, size) => {
  const lines = text.split("\n");
  const longest = Math.max(1, ...lines.map((l) => l.length));
  return { w: longest * size * 0.5, h: lines.length * size * TEXT_LINE_HEIGHT };
};

let measurer: Measurer = approximate;

/** Installs an exact text measurer (canvas based) once the font has loaded. */
export function setTextMeasurer(m: Measurer | null): void {
  measurer = m ?? approximate;
}

export function textSize(el: TextElement): { w: number; h: number } {
  return measurer(el.text, el.size);
}

/** Axis-aligned bounds of an element. */
export function boundsOf(el: BoardElement): Rect {
  switch (el.type) {
    case "rect":
    case "ellipse":
      return { x: el.x, y: el.y, w: el.w, h: el.h };
    case "text": {
      const { w, h } = textSize(el);
      return { x: el.x, y: el.y, w, h };
    }
    case "pen":
    case "arrow": {
      let minX = Infinity;
      let minY = Infinity;
      let maxX = -Infinity;
      let maxY = -Infinity;
      for (let i = 0; i + 1 < el.points.length; i += 2) {
        const px = el.x + (el.points[i] as number);
        const py = el.y + (el.points[i + 1] as number);
        minX = Math.min(minX, px);
        maxX = Math.max(maxX, px);
        minY = Math.min(minY, py);
        maxY = Math.max(maxY, py);
      }
      if (!Number.isFinite(minX)) return { x: el.x, y: el.y, w: 0, h: 0 };
      return { x: minX, y: minY, w: maxX - minX, h: maxY - minY };
    }
  }
}

export function unionBounds(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) return null;
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.w);
    maxY = Math.max(maxY, r.y + r.h);
  }
  return { x: minX, y: minY, w: maxX - minX, h: maxY - minY };
}

function distToSegment(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax;
  const dy = by - ay;
  const len2 = dx * dx + dy * dy;
  const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / len2));
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}

function nearPolyline(el: { x: number; y: number; points: number[]; width: number }, px: number, py: number, tol: number) {
  const reach = tol + el.width / 2;
  const p = el.points;
  if (p.length < 4) {
    return Math.hypot(px - (el.x + (p[0] ?? 0)), py - (el.y + (p[1] ?? 0))) <= reach;
  }
  for (let i = 0; i + 3 < p.length; i += 2) {
    const d = distToSegment(
      px,
      py,
      el.x + (p[i] as number),
      el.y + (p[i + 1] as number),
      el.x + (p[i + 2] as number),
      el.y + (p[i + 3] as number),
    );
    if (d <= reach) return true;
  }
  return false;
}

/**
 * Does the point touch the element? Unfilled shapes are hit on their outline
 * so that shapes drawn inside a rectangle stay selectable.
 */
export function hitTest(el: BoardElement, px: number, py: number, tol: number): boolean {
  switch (el.type) {
    case "pen":
    case "arrow":
      return nearPolyline(el, px, py, tol);
    case "rect": {
      const reach = tol + el.width / 2;
      const inOuter = px >= el.x - reach && px <= el.x + el.w + reach && py >= el.y - reach && py <= el.y + el.h + reach;
      const inInner =
        px > el.x + reach && px < el.x + el.w - reach && py > el.y + reach && py < el.y + el.h - reach;
      return inOuter && !inInner;
    }
    case "ellipse": {
      const rx = el.w / 2;
      const ry = el.h / 2;
      if (rx <= 0 || ry <= 0) return false;
      const cx = el.x + rx;
      const cy = el.y + ry;
      // Normalised radial distance; 1 is exactly on the outline.
      const r = Math.hypot((px - cx) / rx, (py - cy) / ry);
      const reach = (tol + el.width / 2) / Math.min(rx, ry);
      return Math.abs(r - 1) <= reach;
    }
    case "text": {
      const b = boundsOf(el);
      return px >= b.x - tol && px <= b.x + b.w + tol && py >= b.y - tol && py <= b.y + b.h + tol;
    }
  }
}

/** Topmost element under the point, or undefined. `elements` is bottom-to-top. */
export function pick(elements: readonly BoardElement[], px: number, py: number, tol: number): BoardElement | undefined {
  for (let i = elements.length - 1; i >= 0; i--) {
    const el = elements[i] as BoardElement;
    if (hitTest(el, px, py, tol)) return el;
  }
  return undefined;
}

/** Returns a copy of the element moved by (dx, dy). */
export function translated<T extends BoardElement>(el: T, dx: number, dy: number): T {
  return { ...el, x: el.x + dx, y: el.y + dy };
}
