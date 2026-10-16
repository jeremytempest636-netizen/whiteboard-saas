"use client";

import type Konva from "konva";
import {
  forwardRef,
  memo,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { Arrow, Ellipse, Group, Label, Layer, Line, Rect, Stage, Tag, Text } from "react-konva";
import type { CursorStore } from "@/lib/cursorStore";
import {
  TEXT_FONT,
  TEXT_LINE_HEIGHT,
  boundsOf,
  pick,
  setTextMeasurer,
  translated,
  unionBounds,
  type Rect as Box,
} from "@/lib/geometry";
import { newId } from "@/lib/ids";
import type { BoardElement, Change, Peer, TextElement, Tool } from "@/lib/types";

const MIN_SCALE = 0.2;
const MAX_SCALE = 4;
const HANDLE_PX = 9;
const HIT_PX = 8;
const DEFAULT_TEXT_SIZE = 30;
const CURSOR_SEND_MS = 40;

export interface WhiteboardCanvasHandle {
  zoomBy: (factor: number) => void;
  fit: () => void;
}

export interface WhiteboardCanvasProps {
  elements: BoardElement[];
  getElement: (id: string) => BoardElement | undefined;
  tool: Tool;
  color: string;
  strokeWidth: number;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  /** Streams an in-progress shape. */
  onLive: (el: BoardElement) => void;
  /** Finalises edits. */
  onCommit: (changes: Change[]) => void;
  beginGroup: () => void;
  endGroup: () => void;
  /** Remote participants; omit for a local-only board. */
  peers?: Peer[];
  cursors?: CursorStore;
  meId?: string;
  onCursor?: (x: number, y: number) => void;
  onScaleChange?: (scale: number) => void;
  /** Called when the user finishes a gesture, e.g. to leave the text tool. */
  onToolDone?: () => void;
  /**
   * When this number changes (a full board was loaded) and the user has not
   * moved the view yet, the content is brought into view.
   */
  fitSignal?: number;
  className?: string;
}

interface View {
  x: number;
  y: number;
  scale: number;
}

type Handle = "nw" | "ne" | "sw" | "se" | "start" | "end";

type Operation =
  | { k: "pan"; sx: number; sy: number; view: View }
  | { k: "draw"; el: BoardElement; ox: number; oy: number }
  | { k: "erase"; erased: Set<string> }
  | { k: "move"; before: BoardElement; wx: number; wy: number; moved: boolean }
  | { k: "resize"; before: BoardElement; handle: Handle }
  | { k: "text-click"; wx: number; wy: number; sx: number; sy: number }
  | { k: "pinch"; d0: number; world: { x: number; y: number }; scale0: number };

interface Editor {
  id: string | null;
  x: number;
  y: number;
  text: string;
  size: number;
  color: string;
  before?: TextElement;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

// ---------------------------------------------------------------------------
// Rendering of a single element. Memoised on the element object, which the
// board state keeps stable until that element actually changes.
// ---------------------------------------------------------------------------

const Shape = memo(function Shape({ el }: { el: BoardElement }) {
  const common = { listening: false, perfectDrawEnabled: false } as const;
  switch (el.type) {
    case "pen":
      return (
        <Line {...common} x={el.x} y={el.y} points={el.points} stroke={el.color} strokeWidth={el.width} tension={0.35} lineCap="round" lineJoin="round" />
      );
    case "rect":
      return <Rect {...common} x={el.x} y={el.y} width={el.w} height={el.h} stroke={el.color} strokeWidth={el.width} cornerRadius={2} lineJoin="round" />;
    case "ellipse":
      return (
        <Ellipse {...common} x={el.x + el.w / 2} y={el.y + el.h / 2} radiusX={Math.abs(el.w / 2)} radiusY={Math.abs(el.h / 2)} stroke={el.color} strokeWidth={el.width} />
      );
    case "arrow":
      return (
        <Arrow
          {...common}
          x={el.x}
          y={el.y}
          points={el.points}
          stroke={el.color}
          fill={el.color}
          strokeWidth={el.width}
          pointerLength={10 + el.width * 2}
          pointerWidth={9 + el.width * 1.6}
          lineCap="round"
          lineJoin="round"
        />
      );
    case "text":
      return <Text {...common} x={el.x} y={el.y} text={el.text} fontFamily={TEXT_FONT} fontSize={el.size} lineHeight={TEXT_LINE_HEIGHT} fill={el.color} />;
  }
});

function RemoteCursors({ store, peers, meId, scale }: { store: CursorStore; peers: Peer[]; meId?: string; scale: number }) {
  useSyncExternalStore(store.subscribe, store.getVersion, store.getVersion);
  const byId = new Map(peers.map((p) => [p.id, p]));
  const now = performance.now();
  const out: React.ReactNode[] = [];
  for (const [id, c] of store.entries()) {
    const peer = byId.get(id);
    if (!peer || id === meId || now - c.t > 8000) continue;
    out.push(
      <Group key={id} x={c.x} y={c.y} scaleX={1 / scale} scaleY={1 / scale} listening={false} opacity={now - c.t > 4000 ? 0.45 : 1}>
        <Line points={[0, 0, 0, 17, 4.5, 13, 8, 20, 10.5, 19, 7.5, 12, 13, 12]} closed fill={peer.color} stroke="#fff" strokeWidth={1.5} lineJoin="round" />
        <Label x={13} y={17}>
          <Tag fill={peer.color} cornerRadius={4} />
          <Text text={peer.name} fontFamily="Familjen Grotesk Variable, system-ui, sans-serif" fontSize={12} fontStyle="600" fill="#fff" padding={4} />
        </Label>
      </Group>,
    );
  }
  return <>{out}</>;
}

// ---------------------------------------------------------------------------
// Selection handles
// ---------------------------------------------------------------------------

function handlePoints(el: BoardElement): { handle: Handle; x: number; y: number }[] {
  switch (el.type) {
    case "rect":
    case "ellipse":
      return [
        { handle: "nw", x: el.x, y: el.y },
        { handle: "ne", x: el.x + el.w, y: el.y },
        { handle: "sw", x: el.x, y: el.y + el.h },
        { handle: "se", x: el.x + el.w, y: el.y + el.h },
      ];
    case "text": {
      const b = boundsOf(el);
      return [{ handle: "se", x: b.x + b.w, y: b.y + b.h }];
    }
    case "arrow": {
      const [x0 = 0, y0 = 0, x1 = 0, y1 = 0] = el.points;
      return [
        { handle: "start", x: el.x + x0, y: el.y + y0 },
        { handle: "end", x: el.x + x1, y: el.y + y1 },
      ];
    }
    case "pen":
      return [];
  }
}

function Selection({ el, scale }: { el: BoardElement; scale: number }) {
  const b = boundsOf(el);
  const pad = 6 / scale;
  const hs = HANDLE_PX / scale;
  return (
    <Group listening={false}>
      <Rect
        x={b.x - pad}
        y={b.y - pad}
        width={b.w + pad * 2}
        height={b.h + pad * 2}
        stroke="#1b1f23"
        strokeWidth={1 / scale}
        dash={[5 / scale, 4 / scale]}
      />
      {handlePoints(el).map((h) => (
        <Rect key={h.handle} x={h.x - hs / 2} y={h.y - hs / 2} width={hs} height={hs} fill="#f5f6f3" stroke="#1b1f23" strokeWidth={1.5 / scale} cornerRadius={2 / scale} />
      ))}
    </Group>
  );
}

// ---------------------------------------------------------------------------

export const WhiteboardCanvas = memo(
  forwardRef<WhiteboardCanvasHandle, WhiteboardCanvasProps>(function WhiteboardCanvas(props, ref) {
    const { elements, tool, color, strokeWidth, selectedId } = props;

    const hostRef = useRef<HTMLDivElement>(null);
    const [size, setSize] = useState({ w: 0, h: 0 });
    const [view, setView] = useState<View>({ x: 0, y: 0, scale: 1 });
    const viewRef = useRef(view);
    viewRef.current = view;
    const [editor, setEditor] = useState<Editor | null>(null);
    const [fontTick, setFontTick] = useState(0);
    const [spaceDown, setSpaceDown] = useState(false);
    const [panning, setPanning] = useState(false);

    // Latest props for use inside long-lived event handlers.
    const p = useRef(props);
    p.current = props;

    const op = useRef<Operation | null>(null);
    const pointers = useRef(new Map<number, { x: number; y: number }>());
    const lastCursorSent = useRef(0);
    const touched = useRef(false); // user moved the view or drew something
    const lastFitSignal = useRef(0);

    // --- sizing ------------------------------------------------------------
    useLayoutEffect(() => {
      const host = hostRef.current;
      if (!host) return;
      const ro = new ResizeObserver(() => setSize({ w: host.clientWidth, h: host.clientHeight }));
      ro.observe(host);
      setSize({ w: host.clientWidth, h: host.clientHeight });
      return () => ro.disconnect();
    }, []);

    // Centre the board origin the first time we know the size.
    const centred = useRef(false);
    useEffect(() => {
      if (centred.current || size.w === 0) return;
      centred.current = true;
      setView({ x: size.w / 2, y: size.h / 2, scale: 1 });
    }, [size]);

    useEffect(() => {
      props.onScaleChange?.(view.scale);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [view.scale]);

    // --- fonts: text is measured and drawn with the marker typeface -----------
    useEffect(() => {
      let cancelled = false;
      document.fonts
        .load(`${DEFAULT_TEXT_SIZE}px "Caveat Brush"`)
        .catch(() => undefined)
        .then(() => {
          if (cancelled) return;
          const ctx = document.createElement("canvas").getContext("2d");
          if (ctx) {
            setTextMeasurer((text, sizePx) => {
              ctx.font = `${sizePx}px ${TEXT_FONT}`;
              const lines = text.split("\n");
              const w = Math.max(...lines.map((l) => ctx.measureText(l).width), 1);
              return { w, h: lines.length * sizePx * TEXT_LINE_HEIGHT };
            });
          }
          setFontTick((n) => n + 1); // redraw text with the real font
        });
      return () => {
        cancelled = true;
      };
    }, []);

    // --- view helpers ------------------------------------------------------
    const toWorld = useCallback((sx: number, sy: number) => {
      const v = viewRef.current;
      return { x: (sx - v.x) / v.scale, y: (sy - v.y) / v.scale };
    }, []);

    const zoomAt = useCallback((factor: number, sx: number, sy: number) => {
      touched.current = true;
      setView((v) => {
        const scale = clamp(v.scale * factor, MIN_SCALE, MAX_SCALE);
        const k = scale / v.scale;
        return { scale, x: sx - (sx - v.x) * k, y: sy - (sy - v.y) * k };
      });
    }, []);

    const fit = useCallback(() => {
      const box = unionBounds(p.current.elements.map(boundsOf));
      const host = hostRef.current;
      if (!host) return;
      const w = host.clientWidth;
      const h = host.clientHeight;
      if (!box || (box.w === 0 && box.h === 0)) {
        setView({ x: w / 2, y: h / 2, scale: 1 });
        return;
      }
      const margin = 80;
      const scale = clamp(Math.min((w - margin * 2) / Math.max(box.w, 1), (h - margin * 2) / Math.max(box.h, 1)), MIN_SCALE, 1.5);
      setView({ scale, x: w / 2 - (box.x + box.w / 2) * scale, y: h / 2 - (box.y + box.h / 2) * scale });
    }, []);

    useImperativeHandle(
      ref,
      () => ({
        zoomBy: (factor) => {
          const host = hostRef.current;
          if (host) zoomAt(factor, host.clientWidth / 2, host.clientHeight / 2);
        },
        fit,
      }),
      [fit, zoomAt],
    );

    // Bring existing content into view when a board loads, unless the user
    // already panned, zoomed or drew.
    useEffect(() => {
      const signal = props.fitSignal ?? 0;
      if (signal === lastFitSignal.current || size.w === 0) return;
      lastFitSignal.current = signal;
      if (!touched.current && p.current.elements.length > 0) fit();
    }, [props.fitSignal, size.w, fit]);

    // --- wheel: scroll pans, pinch / ctrl+wheel zooms --------------------------
    useEffect(() => {
      const host = hostRef.current;
      if (!host) return;
      const onWheel = (e: WheelEvent) => {
        e.preventDefault();
        const r = host.getBoundingClientRect();
        if (e.ctrlKey || e.metaKey) {
          zoomAt(Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
        } else {
          touched.current = true;
          setView((v) => ({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }));
        }
      };
      host.addEventListener("wheel", onWheel, { passive: false });
      return () => host.removeEventListener("wheel", onWheel);
    }, [zoomAt]);

    // --- space = temporary hand tool -------------------------------------------
    useEffect(() => {
      const typing = (t: EventTarget | null) => t instanceof HTMLElement && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable);
      const down = (e: KeyboardEvent) => {
        if (e.code === "Space" && !typing(e.target)) {
          e.preventDefault();
          setSpaceDown(true);
        }
        if (e.key === "Escape") cancelOperation();
      };
      const up = (e: KeyboardEvent) => {
        if (e.code === "Space") setSpaceDown(false);
      };
      window.addEventListener("keydown", down);
      window.addEventListener("keyup", up);
      return () => {
        window.removeEventListener("keydown", down);
        window.removeEventListener("keyup", up);
      };
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    // --- drawing operations ------------------------------------------------------
    const discard = (id: string) => p.current.onCommit([{ id, before: undefined, after: undefined }]);

    function cancelOperation() {
      const cur = op.current;
      if (!cur) return;
      if (cur.k === "draw") discard(cur.el.id);
      if (cur.k === "erase") p.current.endGroup();
      if (cur.k === "move" || cur.k === "resize") p.current.onLive(cur.before);
      op.current = null;
      setPanning(false);
    }

    const eraseAt = (wx: number, wy: number, erased: Set<string>) => {
      const { elements: els, getElement, onCommit } = p.current;
      const hit = pick(els.filter((e) => !erased.has(e.id)), wx, wy, HIT_PX / viewRef.current.scale);
      if (!hit) return;
      const current = getElement(hit.id);
      if (!current) return;
      erased.add(hit.id);
      onCommit([{ id: hit.id, before: current, after: undefined }]);
    };

    const handleAt = (sx: number, sy: number): Handle | null => {
      const { selectedId: sel, getElement } = p.current;
      const el = sel ? getElement(sel) : undefined;
      if (!el) return null;
      const v = viewRef.current;
      for (const h of handlePoints(el)) {
        if (Math.hypot(h.x * v.scale + v.x - sx, h.y * v.scale + v.y - sy) <= HANDLE_PX + 3) return h.handle;
      }
      return null;
    };

    const openEditor = (e: Editor) => {
      setEditor(e);
      p.current.onSelect(null);
    };

    const local = (e: { clientX: number; clientY: number }) => {
      const r = hostRef.current!.getBoundingClientRect();
      return { sx: e.clientX - r.left, sy: e.clientY - r.top };
    };

    const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
      if (editor) return; // the textarea owns the pointer while editing
      const { sx, sy } = local(e);
      hostRef.current!.setPointerCapture(e.pointerId);
      pointers.current.set(e.pointerId, { x: sx, y: sy });

      // Second finger: switch to pinch-zoom and abandon the stroke.
      if (pointers.current.size === 2) {
        cancelOperation();
        const [a, b] = [...pointers.current.values()] as [{ x: number; y: number }, { x: number; y: number }];
        const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
        op.current = { k: "pinch", d0: Math.hypot(a.x - b.x, a.y - b.y) || 1, world: toWorld(mid.x, mid.y), scale0: viewRef.current.scale };
        return;
      }
      if (pointers.current.size > 2) return;

      touched.current = true;
      const w = toWorld(sx, sy);
      const t = p.current.tool;
      const { color: col, strokeWidth: sw } = p.current;

      if (e.button === 1 || spaceDown || t === "hand") {
        op.current = { k: "pan", sx, sy, view: viewRef.current };
        setPanning(true);
        return;
      }
      if (e.button !== 0 && e.pointerType === "mouse") return;

      switch (t) {
        case "pen": {
          const el: BoardElement = { id: newId(), ts: 0, z: Date.now(), type: "pen", x: w.x, y: w.y, color: col, width: sw, points: [0, 0, 0.01, 0.01] };
          op.current = { k: "draw", el, ox: w.x, oy: w.y };
          p.current.onLive(el);
          break;
        }
        case "rect":
        case "ellipse": {
          const el: BoardElement = { id: newId(), ts: 0, z: Date.now(), type: t, x: w.x, y: w.y, w: 0, h: 0, color: col, width: sw };
          op.current = { k: "draw", el, ox: w.x, oy: w.y };
          p.current.onLive(el);
          break;
        }
        case "arrow": {
          const el: BoardElement = { id: newId(), ts: 0, z: Date.now(), type: "arrow", x: w.x, y: w.y, color: col, width: sw, points: [0, 0, 0, 0] };
          op.current = { k: "draw", el, ox: w.x, oy: w.y };
          p.current.onLive(el);
          break;
        }
        case "text":
          op.current = { k: "text-click", wx: w.x, wy: w.y, sx, sy };
          break;
        case "eraser": {
          p.current.beginGroup();
          const erased = new Set<string>();
          op.current = { k: "erase", erased };
          eraseAt(w.x, w.y, erased);
          break;
        }
        case "select": {
          const handle = handleAt(sx, sy);
          const sel = p.current.selectedId ? p.current.getElement(p.current.selectedId) : undefined;
          if (handle && sel) {
            op.current = { k: "resize", before: sel, handle };
            break;
          }
          const hit = pick(p.current.elements, w.x, w.y, HIT_PX / viewRef.current.scale);
          p.current.onSelect(hit?.id ?? null);
          if (hit) op.current = { k: "move", before: hit, wx: w.x, wy: w.y, moved: false };
          break;
        }
      }
    };

    const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
      const { sx, sy } = local(e);
      if (pointers.current.has(e.pointerId)) pointers.current.set(e.pointerId, { x: sx, y: sy });
      const w = toWorld(sx, sy);

      const now = performance.now();
      if (p.current.onCursor && now - lastCursorSent.current > CURSOR_SEND_MS && (e.pointerType === "mouse" || e.buttons)) {
        lastCursorSent.current = now;
        p.current.onCursor(w.x, w.y);
      }

      const cur = op.current;
      if (!cur) return;

      switch (cur.k) {
        case "pinch": {
          if (pointers.current.size < 2) return;
          const [a, b] = [...pointers.current.values()] as [{ x: number; y: number }, { x: number; y: number }];
          const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
          const scale = clamp(cur.scale0 * (Math.hypot(a.x - b.x, a.y - b.y) / cur.d0), MIN_SCALE, MAX_SCALE);
          setView({ scale, x: mid.x - cur.world.x * scale, y: mid.y - cur.world.y * scale });
          return;
        }
        case "pan":
          setView({ ...cur.view, x: cur.view.x + (sx - cur.sx), y: cur.view.y + (sy - cur.sy) });
          return;
        case "erase":
          eraseAt(w.x, w.y, cur.erased);
          return;
        case "draw": {
          const el = cur.el;
          let next: BoardElement = el;
          if (el.type === "pen") {
            const rx = w.x - el.x;
            const ry = w.y - el.y;
            const n = el.points.length;
            const lx = el.points[n - 2] as number;
            const ly = el.points[n - 1] as number;
            if (Math.hypot(rx - lx, ry - ly) < 1.2 / viewRef.current.scale) return;
            // The first real point replaces the dot placeholder.
            const base = n === 4 && el.points[2] === 0.01 ? el.points.slice(0, 2) : el.points;
            next = { ...el, points: [...base, rx, ry] };
          } else if (el.type === "rect" || el.type === "ellipse") {
            let dx = w.x - cur.ox;
            let dy = w.y - cur.oy;
            if (e.shiftKey) {
              const m = Math.max(Math.abs(dx), Math.abs(dy));
              dx = Math.sign(dx || 1) * m;
              dy = Math.sign(dy || 1) * m;
            }
            next = { ...el, x: Math.min(cur.ox, cur.ox + dx), y: Math.min(cur.oy, cur.oy + dy), w: Math.abs(dx), h: Math.abs(dy) };
          } else if (el.type === "arrow") {
            let dx = w.x - cur.ox;
            let dy = w.y - cur.oy;
            if (e.shiftKey) {
              const len = Math.hypot(dx, dy);
              const step = Math.PI / 12;
              const ang = Math.round(Math.atan2(dy, dx) / step) * step;
              dx = Math.cos(ang) * len;
              dy = Math.sin(ang) * len;
            }
            next = { ...el, points: [0, 0, dx, dy] };
          }
          cur.el = next;
          p.current.onLive(next);
          return;
        }
        case "move": {
          const dx = w.x - cur.wx;
          const dy = w.y - cur.wy;
          if (!cur.moved && Math.hypot(dx, dy) * viewRef.current.scale < 3) return;
          cur.moved = true;
          p.current.onLive(translated(cur.before, dx, dy));
          return;
        }
        case "resize": {
          const el = cur.before;
          let next: BoardElement = el;
          if (el.type === "rect" || el.type === "ellipse") {
            const fx = cur.handle === "nw" || cur.handle === "sw" ? el.x + el.w : el.x;
            const fy = cur.handle === "nw" || cur.handle === "ne" ? el.y + el.h : el.y;
            next = { ...el, x: Math.min(fx, w.x), y: Math.min(fy, w.y), w: Math.max(4, Math.abs(w.x - fx)), h: Math.max(4, Math.abs(w.y - fy)) };
          } else if (el.type === "text") {
            const lines = el.text.split("\n").length;
            next = { ...el, size: clamp(Math.round((w.y - el.y) / (lines * TEXT_LINE_HEIGHT)), 10, 240) };
          } else if (el.type === "arrow") {
            const rx = w.x - el.x;
            const ry = w.y - el.y;
            const [x0 = 0, y0 = 0, x1 = 0, y1 = 0] = el.points;
            next = { ...el, points: cur.handle === "start" ? [rx, ry, x1, y1] : [x0, y0, rx, ry] };
          }
          p.current.onLive(next);
          return;
        }
      }
    };

    const finish = (e: React.PointerEvent<HTMLDivElement>) => {
      pointers.current.delete(e.pointerId);
      const cur = op.current;
      if (!cur) return;

      if (cur.k === "pinch") {
        if (pointers.current.size < 2) op.current = null;
        return;
      }
      op.current = null;
      setPanning(false);

      switch (cur.k) {
        case "erase":
          p.current.endGroup();
          return;
        case "draw": {
          const el = cur.el;
          const tiny =
            ((el.type === "rect" || el.type === "ellipse") && el.w < 4 && el.h < 4) ||
            (el.type === "arrow" && Math.hypot(el.points[2] ?? 0, el.points[3] ?? 0) < 6);
          if (tiny) discard(el.id);
          else p.current.onCommit([{ id: el.id, before: undefined, after: el }]);
          if (el.type !== "pen") p.current.onToolDone?.();
          return;
        }
        case "move": {
          if (!cur.moved) return;
          const after = p.current.getElement(cur.before.id);
          if (after) p.current.onCommit([{ id: cur.before.id, before: cur.before, after }]);
          return;
        }
        case "resize": {
          const after = p.current.getElement(cur.before.id);
          if (after) p.current.onCommit([{ id: cur.before.id, before: cur.before, after }]);
          return;
        }
        case "text-click":
          if (Math.hypot(local(e).sx - cur.sx, local(e).sy - cur.sy) < 6) {
            openEditor({ id: null, x: cur.wx, y: cur.wy, text: "", size: DEFAULT_TEXT_SIZE, color: p.current.color });
          }
          return;
      }
    };

    const onDoubleClick = (e: React.MouseEvent<HTMLDivElement>) => {
      if (p.current.tool !== "select") return;
      const { sx, sy } = local(e);
      const w = toWorld(sx, sy);
      const hit = pick(p.current.elements, w.x, w.y, HIT_PX / viewRef.current.scale);
      if (hit?.type === "text") {
        openEditor({ id: hit.id, x: hit.x, y: hit.y, text: hit.text, size: hit.size, color: hit.color, before: hit });
      }
    };

    // --- text editor ---------------------------------------------------------------
    const closeEditor = (commit: boolean, value: string) => {
      const ed = editor;
      setEditor(null);
      if (!ed || !commit) return;
      const text = value.replace(/\s+$/, "");
      if (ed.before) {
        if (text.trim() === "") p.current.onCommit([{ id: ed.before.id, before: ed.before, after: undefined }]);
        else if (text !== ed.before.text) p.current.onCommit([{ id: ed.before.id, before: ed.before, after: { ...ed.before, text } }]);
        return;
      }
      if (text.trim() === "") return;
      const el: TextElement = { id: newId(), ts: 0, z: Date.now(), type: "text", x: ed.x, y: ed.y, text, size: ed.size, color: ed.color };
      p.current.onCommit([{ id: el.id, before: undefined, after: el }]);
      p.current.onToolDone?.();
    };

    // --- cursor & background ------------------------------------------------------------
    const effectiveTool: Tool = spaceDown ? "hand" : tool;
    const cursor =
      effectiveTool === "hand"
        ? panning
          ? "grabbing"
          : "grab"
        : effectiveTool === "text"
          ? "text"
          : effectiveTool === "select"
            ? "default"
            : effectiveTool === "eraser"
              ? `url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='26' height='26'><circle cx='13' cy='13' r='10' fill='white' fill-opacity='.5' stroke='%231b1f23' stroke-width='1.5'/></svg>") 13 13, cell`
              : "crosshair";

    const grid = Math.max(14, 28 * view.scale);
    const selected = selectedId ? props.getElement(selectedId) : undefined;
    const hiddenId = editor?.id ?? null;
    const shown = hiddenId ? elements.filter((e) => e.id !== hiddenId) : elements;

    return (
      <div
        ref={hostRef}
        className={props.className}
        style={{
          cursor,
          touchAction: "none",
          position: "relative",
          width: "100%",
          height: "100%",
          backgroundImage: "radial-gradient(circle, rgba(27,31,35,0.16) 1px, transparent 1.3px)",
          backgroundSize: `${grid}px ${grid}px`,
          backgroundPosition: `${view.x}px ${view.y}px`,
        }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={finish}
        onPointerCancel={finish}
        onDoubleClick={onDoubleClick}
        onContextMenu={(e) => e.preventDefault()}
      >
        {size.w > 0 && (
          <Stage width={size.w} height={size.h} x={view.x} y={view.y} scaleX={view.scale} scaleY={view.scale} listening={false}>
            <Layer key={fontTick} listening={false}>
              {shown.map((el) => (
                <Shape key={el.id} el={el} />
              ))}
            </Layer>
            <Layer listening={false}>
              {selected && !editor && tool === "select" && <Selection el={selected} scale={view.scale} />}
              {props.cursors && <RemoteCursors store={props.cursors} peers={props.peers ?? []} meId={props.meId} scale={view.scale} />}
            </Layer>
          </Stage>
        )}

        {editor && (
          <TextEditor
            editor={editor}
            view={view}
            onDone={closeEditor}
          />
        )}
      </div>
    );
  }),
);

function TextEditor({ editor, view, onDone }: { editor: Editor; view: View; onDone: (commit: boolean, value: string) => void }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const [value, setValue] = useState(editor.text);
  const doneRef = useRef(false);

  useEffect(() => {
    const t = ref.current;
    if (!t) return;
    t.focus();
    t.setSelectionRange(t.value.length, t.value.length);
  }, []);

  useLayoutEffect(() => {
    const t = ref.current;
    if (!t) return;
    t.style.height = "0px";
    t.style.height = `${t.scrollHeight}px`;
    t.style.width = "0px";
    t.style.width = `${Math.max(t.scrollWidth + 6, 40)}px`;
  }, [value, view.scale]);

  const finish = (commit: boolean) => {
    if (doneRef.current) return;
    doneRef.current = true;
    onDone(commit, value);
  };

  const fontSize = editor.size * view.scale;
  return (
    <textarea
      ref={ref}
      value={value}
      aria-label="Text on the board"
      spellCheck={false}
      onChange={(e) => setValue(e.target.value)}
      onBlur={() => finish(true)}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape") finish(false);
        if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) finish(true);
      }}
      onPointerDown={(e) => e.stopPropagation()}
      style={{
        position: "absolute",
        left: editor.x * view.scale + view.x,
        top: editor.y * view.scale + view.y,
        minWidth: 40,
        margin: 0,
        padding: 0,
        border: "none",
        outline: "1.5px dashed rgba(27,31,35,.55)",
        outlineOffset: 4,
        background: "transparent",
        resize: "none",
        overflow: "hidden",
        whiteSpace: "pre",
        color: editor.color,
        fontFamily: TEXT_FONT,
        fontSize,
        lineHeight: TEXT_LINE_HEIGHT,
      }}
    />
  );
}
