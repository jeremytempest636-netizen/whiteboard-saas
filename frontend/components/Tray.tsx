"use client";

import type { ReactNode } from "react";
import type { Tool } from "@/lib/types";
import {
  ArrowIcon,
  DownloadIcon,
  EllipseIcon,
  EraserIcon,
  FitIcon,
  HandIcon,
  MinusIcon,
  PenIcon,
  PlusIcon,
  RectIcon,
  RedoIcon,
  SelectIcon,
  TextIcon,
  TrashIcon,
  UndoIcon,
} from "./Icons";

export const MARKERS = [
  { name: "Black", value: "#1b1f23" },
  { name: "Blue", value: "#1f4fd8" },
  { name: "Red", value: "#d93b30" },
  { name: "Green", value: "#1e8a5a" },
  { name: "Orange", value: "#e08a00" },
  { name: "Purple", value: "#7a3fb5" },
] as const;

export const WIDTHS = [
  { name: "Thin", value: 2 },
  { name: "Medium", value: 4 },
  { name: "Thick", value: 8 },
] as const;

const TOOLS: { id: Tool; label: string; key: string; icon: ReactNode }[] = [
  { id: "select", label: "Select and move", key: "V", icon: <SelectIcon /> },
  { id: "pen", label: "Pen", key: "P", icon: <PenIcon /> },
  { id: "rect", label: "Rectangle", key: "R", icon: <RectIcon /> },
  { id: "ellipse", label: "Ellipse", key: "O", icon: <EllipseIcon /> },
  { id: "arrow", label: "Arrow", key: "A", icon: <ArrowIcon /> },
  { id: "text", label: "Text", key: "T", icon: <TextIcon /> },
  { id: "eraser", label: "Eraser", key: "E", icon: <EraserIcon /> },
  { id: "hand", label: "Pan (or hold Space)", key: "H", icon: <HandIcon /> },
];

function Marker({ color, name, active, onPick }: { color: string; name: string; active: boolean; onPick: () => void }) {
  return (
    <button type="button" className="marker" aria-pressed={active} aria-label={`${name} marker`} title={`${name} marker`} onClick={onPick}>
      <svg width="26" height="52" viewBox="0 0 26 52" aria-hidden="true">
        {/* barrel */}
        <rect x="6" y="15" width="14" height="35" rx="3.5" fill="#eceeec" stroke="rgba(27,31,35,.4)" />
        <rect x="6" y="38" width="14" height="7" fill={color} opacity=".9" />
        {/* cap */}
        <rect x="4.5" y="2" width="17" height="19" rx="5" fill={color} stroke="rgba(27,31,35,.35)" />
        <rect x="8" y="5" width="3" height="11" rx="1.5" fill="#fff" opacity=".28" />
      </svg>
    </button>
  );
}

export interface TrayProps {
  tool: Tool;
  onTool: (t: Tool) => void;
  color: string;
  onColor: (c: string) => void;
  width: number;
  onWidth: (w: number) => void;
  canUndo: boolean;
  canRedo: boolean;
  onUndo: () => void;
  onRedo: () => void;
  onClear: () => void;
  onExport?: () => void;
  zoom?: number;
  onZoomIn?: () => void;
  onZoomOut?: () => void;
  onFit?: () => void;
  /** Landing page variant: markers, eraser and clear only. */
  compact?: boolean;
}

export function Tray(p: TrayProps) {
  const tools = p.compact ? TOOLS.filter((t) => t.id === "pen" || t.id === "eraser") : TOOLS;

  return (
    <div className="tray max-w-[calc(100vw-24px)] flex-wrap justify-center lg:flex-nowrap" role="toolbar" aria-label="Whiteboard tools">
      <div className="flex items-center gap-0.5 self-center">
        {tools.map((t) => (
          <button
            key={t.id}
            type="button"
            className="tool-btn"
            aria-pressed={p.tool === t.id}
            aria-label={t.label}
            title={`${t.label} (${t.key})`}
            onClick={() => p.onTool(t.id)}
          >
            {t.icon}
          </button>
        ))}
      </div>

      <div className="tray-divider" />

      <div className="flex items-end gap-1 px-1 pt-3" role="group" aria-label="Marker colour">
        {MARKERS.map((m) => (
          <Marker key={m.value} color={m.value} name={m.name} active={p.color === m.value} onPick={() => p.onColor(m.value)} />
        ))}
      </div>

      {!p.compact && (
        <>
          <div className="tray-divider" />
          <div className="flex items-center gap-0.5 self-center" role="group" aria-label="Line width">
            {WIDTHS.map((w) => (
              <button
                key={w.value}
                type="button"
                className="tool-btn"
                aria-pressed={p.width === w.value}
                aria-label={`${w.name} line`}
                title={`${w.name} line`}
                onClick={() => p.onWidth(w.value)}
              >
                <span className="block rounded-full bg-ink" style={{ width: w.value + 4, height: w.value + 4 }} />
              </button>
            ))}
          </div>
        </>
      )}

      <div className="tray-divider" />

      <div className="flex items-center gap-0.5 self-center">
        {!p.compact && (
          <>
            <button type="button" className="tool-btn" disabled={!p.canUndo} aria-label="Undo" title="Undo (Ctrl+Z)" onClick={p.onUndo}>
              <UndoIcon />
            </button>
            <button type="button" className="tool-btn" disabled={!p.canRedo} aria-label="Redo" title="Redo (Ctrl+Shift+Z)" onClick={p.onRedo}>
              <RedoIcon />
            </button>
          </>
        )}
        <button type="button" className="tool-btn" aria-label="Clear board" title="Clear board" onClick={p.onClear}>
          <TrashIcon />
        </button>
        {p.onExport && (
          <button type="button" className="tool-btn" aria-label="Download as PNG" title="Download as PNG" onClick={p.onExport}>
            <DownloadIcon />
          </button>
        )}
      </div>

      {!p.compact && p.onZoomIn && p.onZoomOut && p.onFit && (
        <>
          <div className="tray-divider" />
          <div className="flex items-center gap-0.5 self-center">
            <button type="button" className="tool-btn" aria-label="Zoom out" title="Zoom out" onClick={p.onZoomOut}>
              <MinusIcon />
            </button>
            <span className="w-11 text-center text-sm tabular-nums text-ink-soft" aria-live="off">
              {Math.round((p.zoom ?? 1) * 100)}%
            </span>
            <button type="button" className="tool-btn" aria-label="Zoom in" title="Zoom in" onClick={p.onZoomIn}>
              <PlusIcon />
            </button>
            <button type="button" className="tool-btn" aria-label="Fit board to screen" title="Fit to screen" onClick={p.onFit}>
              <FitIcon />
            </button>
          </div>
        </>
      )}
    </div>
  );
}
