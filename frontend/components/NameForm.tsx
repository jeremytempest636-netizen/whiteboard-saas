"use client";

import { useId, useState } from "react";
import { ApiError } from "@/lib/api";

/** Asks for a display name. Used when joining a board without a session. */
export function NameForm({
  submitLabel,
  onSubmit,
}: {
  submitLabel: string;
  onSubmit: (name: string) => Promise<void>;
}) {
  const id = useId();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setError("Enter a name so others can tell who is drawing.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await onSubmit(name.trim());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong. Try again.");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
      <label htmlFor={id} className="font-medium">
        Your name
      </label>
      <input
        id={id}
        value={name}
        onChange={(e) => setName(e.target.value)}
        maxLength={40}
        autoComplete="nickname"
        autoFocus
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-err` : undefined}
        className="h-11 rounded-lg border-[1.5px] border-ink bg-board px-3 text-base outline-offset-2"
        placeholder="Dewi"
      />
      {error && (
        <p id={`${id}-err`} role="alert" className="text-sm text-m-red">
          {error}
        </p>
      )}
      <button
        type="submit"
        disabled={busy}
        className="h-11 rounded-lg bg-ink px-5 font-medium text-board hover:bg-black disabled:opacity-60"
      >
        {busy ? "One moment…" : submitLabel}
      </button>
    </form>
  );
}
