"use client";

import { useEffect, useRef, useState } from "react";
import type { ConnectionStatus, Peer } from "@/lib/types";
import { LinkIcon } from "./Icons";

const STATUS: Record<ConnectionStatus, { label: string; color: string }> = {
  live: { label: "Live", color: "#1e8a5a" },
  connecting: { label: "Connecting", color: "#e08a00" },
  reconnecting: { label: "Reconnecting", color: "#e08a00" },
  offline: { label: "Offline", color: "#d93b30" },
};

export function ConnectionBadge({ status, onRetry }: { status: ConnectionStatus; onRetry: () => void }) {
  const s = STATUS[status];
  return (
    <div className="pill flex items-center gap-2 py-1 pr-3 pl-2.5 text-sm" role="status">
      <span className="size-2 rounded-full" style={{ background: s.color }} aria-hidden="true" />
      <span>{s.label}</span>
      {status === "offline" && (
        <button type="button" className="font-medium underline underline-offset-2" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const letters = parts.length > 1 ? (parts[0]![0] ?? "") + (parts[parts.length - 1]![0] ?? "") : name.trim().slice(0, 2);
  return letters.toUpperCase();
}

export function PresenceStack({ me, peers }: { me: Peer | null; peers: Peer[] }) {
  const all = [...(me ? [{ ...me, name: `${me.name} (you)` }] : []), ...peers];
  const shown = all.slice(0, 5);
  const extra = all.length - shown.length;
  return (
    <ul className="flex items-center pl-2" aria-label={`${all.length} ${all.length === 1 ? "person" : "people"} on this board`}>
      {shown.map((p) => (
        <li
          key={p.id}
          title={p.name}
          className="-ml-2 grid size-8 place-items-center rounded-full border-2 border-wall text-xs font-semibold text-white"
          style={{ background: p.color }}
        >
          {initials(p.name.replace(" (you)", ""))}
        </li>
      ))}
      {extra > 0 && (
        <li className="-ml-2 grid size-8 place-items-center rounded-full border-2 border-wall bg-ink text-xs font-semibold text-board">+{extra}</li>
      )}
    </ul>
  );
}

export function CopyLinkButton() {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
    } catch {
      const input = document.createElement("input");
      input.value = window.location.href;
      document.body.appendChild(input);
      input.select();
      document.execCommand("copy");
      input.remove();
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 2000);
  };

  return (
    <button
      type="button"
      onClick={copy}
      className="flex h-9 items-center gap-2 rounded-lg bg-ink px-3.5 text-sm font-medium text-board hover:bg-black"
    >
      <LinkIcon />
      <span aria-live="polite">{copied ? "Link copied" : "Copy link"}</span>
    </button>
  );
}

export function ConfirmClear({ onCancel, onConfirm }: { onCancel: () => void; onConfirm: () => void }) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    cancelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div className="absolute inset-0 z-20 grid place-items-center bg-ink/25 p-4" role="presentation" onClick={onCancel}>
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="clear-title"
        aria-describedby="clear-desc"
        className="w-full max-w-sm rounded-xl bg-board p-5 shadow-2xl ring-1 ring-ink/20"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="clear-title" className="text-lg font-semibold">
          Clear the whole board?
        </h2>
        <p id="clear-desc" className="mt-1 text-ink-soft">
          Everything on it disappears for everyone, and it can't be undone.
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <button ref={cancelRef} type="button" className="h-10 rounded-lg px-4 font-medium ring-1 ring-ink/30 hover:bg-ink/5" onClick={onCancel}>
            Keep board
          </button>
          <button type="button" className="h-10 rounded-lg bg-m-red px-4 font-medium text-white hover:brightness-95" onClick={onConfirm}>
            Clear board
          </button>
        </div>
      </div>
    </div>
  );
}
