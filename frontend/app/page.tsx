"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useId, useRef, useState } from "react";
import { NameForm } from "@/components/NameForm";
import { Tray } from "@/components/Tray";
import { WhiteboardCanvas } from "@/components/WhiteboardCanvas";
import { useBoard } from "@/hooks/useBoard";
import { useSession } from "@/hooks/useSession";
import { api, ApiError, type Board } from "@/lib/api";
import { parseBoardCode } from "@/lib/session";
import type { Change, Tool } from "@/lib/types";

export default function Home() {
  const router = useRouter();
  const { ready, session, signIn, signOut } = useSession();

  // The board behind the form is a real, local-only whiteboard.
  const local = useBoard("", null);
  const [tool, setTool] = useState<Tool>("pen");
  const [color, setColor] = useState("#1f4fd8");
  const [drawn, setDrawn] = useState(false);

  const commit = (changes: Change[]) => {
    local.commit(changes);
    if (changes.some((c) => c.after)) setDrawn(true);
  };

  const [recent, setRecent] = useState<Board[]>([]);
  useEffect(() => {
    if (!session) return;
    let cancelled = false;
    api
      .listBoards(session.token)
      .then((b) => !cancelled && setRecent(b.slice(0, 4)))
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [session]);

  const start = async (name: string) => {
    const s = session ?? (await signIn(name));
    const board = await api.createBoard(s.token, `${s.user.displayName}'s board`);
    router.push(`/board/${board.id}`);
  };

  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const startSignedIn = async () => {
    setStarting(true);
    setStartError(null);
    try {
      await start("");
    } catch (err) {
      setStartError(err instanceof ApiError ? err.message : "Couldn't create the board. Try again.");
      setStarting(false);
    }
  };

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-[1180px] flex-col px-4 pt-6 pb-24 sm:px-8">
      <header className="flex items-center justify-between pb-5">
        <span className="text-lg font-semibold tracking-tight">Whiteboard</span>
        {session && (
          <p className="text-sm text-ink-soft">
            Signed in as <strong className="font-semibold text-ink">{session.user.displayName}</strong>{" "}
            <button type="button" onClick={signOut} className="underline underline-offset-2">
              Change
            </button>
          </p>
        )}
      </header>

      <div className="board-frame mb-10">
        <div className="board-surface" style={{ height: "min(74vh, 720px)", minHeight: 560 }}>
          <WhiteboardCanvas
            elements={local.elements}
            getElement={local.getElement}
            tool={tool}
            color={color}
            strokeWidth={4}
            selectedId={null}
            onSelect={() => undefined}
            onLive={local.live}
            onCommit={commit}
            beginGroup={local.beginGroup}
            endGroup={local.endGroup}
          />

          {/* Content sits above the canvas; only the controls take the pointer. */}
          <div className="pointer-events-none absolute inset-0 flex flex-col justify-between p-6 sm:p-10">
            <div className="max-w-[34rem]">
              <h1 className="text-[clamp(2.25rem,5.2vw,4rem)] leading-[1.02] font-bold tracking-[-0.03em] text-balance">
                Think out loud on the same board.
              </h1>
              <p className="mt-4 max-w-[30rem] text-lg text-ink-soft">
                Open a board, send the link, and every pen shows up as it moves. Nothing to install.
              </p>

              <div className="pointer-events-auto mt-7 w-full max-w-sm rounded-xl bg-board/95 p-4 ring-1 ring-ink/15 backdrop-blur-[2px]">
                {!ready ? (
                  <div className="h-[152px]" aria-hidden="true" />
                ) : session ? (
                  <div className="flex flex-col gap-3">
                    <button
                      type="button"
                      onClick={startSignedIn}
                      disabled={starting}
                      className="h-11 rounded-lg bg-ink px-5 font-medium text-board hover:bg-black disabled:opacity-60"
                    >
                      {starting ? "Opening…" : "Start a new board"}
                    </button>
                    {startError && (
                      <p role="alert" className="text-sm text-m-red">
                        {startError}
                      </p>
                    )}
                  </div>
                ) : (
                  <NameForm submitLabel="Start a board" onSubmit={start} />
                )}
                <JoinForm />
              </div>
            </div>

            <div className="flex items-end justify-between gap-6">
              {recent.length > 0 ? (
                <nav aria-label="Your boards" className="pointer-events-auto max-w-xs rounded-xl bg-board/95 p-3 ring-1 ring-ink/15">
                  <p className="px-2 pb-1 text-sm font-semibold">Your boards</p>
                  <ul>
                    {recent.map((b) => (
                      <li key={b.id}>
                        <Link href={`/board/${b.id}`} className="flex items-baseline justify-between gap-4 rounded-md px-2 py-1.5 hover:bg-ink/5">
                          <span className="truncate">{b.title}</span>
                          <time className="shrink-0 text-xs text-ink-soft" dateTime={b.updatedAt}>
                            {new Date(b.updatedAt).toLocaleDateString(undefined, { month: "short", day: "numeric" })}
                          </time>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </nav>
              ) : (
                <span />
              )}
              <p
                className="handwritten pointer-events-none hidden text-3xl text-ink-soft transition-opacity duration-500 sm:block"
                style={{ opacity: drawn ? 0 : 0.8, transform: "rotate(-3deg)" }}
                aria-hidden="true"
              >
                this board is yours. scribble on it.
              </p>
            </div>
          </div>
        </div>

        <div className="absolute bottom-0 left-1/2 z-10 -translate-x-1/2 translate-y-[62%]">
          <Tray
            compact
            tool={tool}
            onTool={setTool}
            color={color}
            onColor={(c) => {
              setColor(c);
              if (tool === "eraser") setTool("pen");
            }}
            width={4}
            onWidth={() => undefined}
            canUndo={false}
            canRedo={false}
            onUndo={() => undefined}
            onRedo={() => undefined}
            onClear={() => {
              local.clearBoard();
              setDrawn(false);
            }}
          />
        </div>
      </div>
    </main>
  );
}

function JoinForm() {
  const router = useRouter();
  const id = useId();
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const code = parseBoardCode(value);
    if (!code) {
      setError("That doesn't look like a board link or code.");
      input.current?.focus();
      return;
    }
    router.push(`/board/${code}`);
  };

  return (
    <form onSubmit={submit} className="mt-4 border-t border-ink/15 pt-4" noValidate>
      <label htmlFor={id} className="text-sm font-medium">
        Have a link?
      </label>
      <div className="mt-1.5 flex gap-2">
        <input
          ref={input}
          id={id}
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            setError(null);
          }}
          placeholder="Paste a board link or code"
          aria-invalid={error ? true : undefined}
          className="h-10 min-w-0 flex-1 rounded-lg border-[1.5px] border-ink/60 bg-board px-3 text-base"
        />
        <button type="submit" className="h-10 rounded-lg px-4 font-medium ring-1 ring-ink/40 hover:bg-ink/5">
          Join
        </button>
      </div>
      {error && (
        <p role="alert" className="mt-1.5 text-sm text-m-red">
          {error}
        </p>
      )}
    </form>
  );
}
