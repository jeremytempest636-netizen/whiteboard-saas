import { TEXT_FONT, TEXT_LINE_HEIGHT, boundsOf, unionBounds } from "./geometry";
import type { BoardElement } from "./types";

const PADDING = 48;
const MAX_SIDE = 8192;

/**
 * Renders the whole board (not just the visible part) to a PNG data URL with a
 * plain 2D canvas. Returns null for an empty board.
 */
export function exportBoardPng(elements: readonly BoardElement[]): string | null {
  const box = unionBounds(elements.map(boundsOf));
  if (!box) return null;

  const w = Math.max(1, box.w + PADDING * 2);
  const h = Math.max(1, box.h + PADDING * 2);
  const scale = Math.min(2, MAX_SIDE / w, MAX_SIDE / h);

  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(w * scale);
  canvas.height = Math.ceil(h * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;

  ctx.scale(scale, scale);
  ctx.fillStyle = "#f5f6f3";
  ctx.fillRect(0, 0, w, h);
  ctx.translate(PADDING - box.x, PADDING - box.y);
  ctx.lineCap = "round";
  ctx.lineJoin = "round";

  for (const el of elements) drawElement(ctx, el);
  return canvas.toDataURL("image/png");
}

function smoothLine(ctx: CanvasRenderingContext2D, ox: number, oy: number, pts: number[]) {
  ctx.beginPath();
  if (pts.length < 4) {
    ctx.moveTo(ox + (pts[0] ?? 0), oy + (pts[1] ?? 0));
    ctx.lineTo(ox + (pts[0] ?? 0) + 0.01, oy + (pts[1] ?? 0) + 0.01);
    return;
  }
  ctx.moveTo(ox + (pts[0] as number), oy + (pts[1] as number));
  for (let i = 2; i + 3 < pts.length; i += 2) {
    const cx = ox + (pts[i] as number);
    const cy = oy + (pts[i + 1] as number);
    const nx = ox + ((pts[i] as number) + (pts[i + 2] as number)) / 2;
    const ny = oy + ((pts[i + 1] as number) + (pts[i + 3] as number)) / 2;
    ctx.quadraticCurveTo(cx, cy, nx, ny);
  }
  ctx.lineTo(ox + (pts[pts.length - 2] as number), oy + (pts[pts.length - 1] as number));
}

function drawElement(ctx: CanvasRenderingContext2D, el: BoardElement) {
  ctx.strokeStyle = el.color;
  ctx.fillStyle = el.color;
  switch (el.type) {
    case "pen":
      ctx.lineWidth = el.width;
      smoothLine(ctx, el.x, el.y, el.points);
      ctx.stroke();
      break;
    case "rect":
      ctx.lineWidth = el.width;
      ctx.strokeRect(el.x, el.y, el.w, el.h);
      break;
    case "ellipse":
      ctx.lineWidth = el.width;
      ctx.beginPath();
      ctx.ellipse(el.x + el.w / 2, el.y + el.h / 2, Math.abs(el.w / 2), Math.abs(el.h / 2), 0, 0, Math.PI * 2);
      ctx.stroke();
      break;
    case "arrow": {
      const [x0 = 0, y0 = 0, x1 = 0, y1 = 0] = el.points;
      const ax = el.x + x0;
      const ay = el.y + y0;
      const bx = el.x + x1;
      const by = el.y + y1;
      ctx.lineWidth = el.width;
      ctx.beginPath();
      ctx.moveTo(ax, ay);
      ctx.lineTo(bx, by);
      ctx.stroke();
      const head = 10 + el.width * 2;
      const angle = Math.atan2(by - ay, bx - ax);
      ctx.beginPath();
      ctx.moveTo(bx, by);
      ctx.lineTo(bx - head * Math.cos(angle - 0.45), by - head * Math.sin(angle - 0.45));
      ctx.moveTo(bx, by);
      ctx.lineTo(bx - head * Math.cos(angle + 0.45), by - head * Math.sin(angle + 0.45));
      ctx.stroke();
      break;
    }
    case "text": {
      ctx.font = `${el.size}px ${TEXT_FONT}`;
      ctx.textBaseline = "top";
      el.text.split("\n").forEach((line, i) => ctx.fillText(line, el.x, el.y + i * el.size * TEXT_LINE_HEIGHT));
      break;
    }
  }
}
