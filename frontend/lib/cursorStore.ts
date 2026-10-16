/**
 * Remote cursors change dozens of times per second. Keeping them in React state
 * would re-render the whole board on every move, so they live in a tiny
 * external store that only the cursor layer subscribes to.
 */
export interface CursorState {
  x: number;
  y: number;
  /** performance.now() of the last update, used to fade idle cursors. */
  t: number;
}

export interface CursorStore {
  set(id: string, x: number, y: number): void;
  remove(id: string): void;
  clear(): void;
  entries(): IterableIterator<[string, CursorState]>;
  subscribe(listener: () => void): () => void;
  getVersion(): number;
}

export function createCursorStore(): CursorStore {
  const map = new Map<string, CursorState>();
  const listeners = new Set<() => void>();
  let version = 0;
  let frame = 0;
  let idleTimer: ReturnType<typeof setInterval> | undefined;

  const emit = () => {
    version++;
    listeners.forEach((l) => l());
  };
  const schedule = () => {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      emit();
    });
  };

  return {
    set(id, x, y) {
      map.set(id, { x, y, t: performance.now() });
      schedule();
    },
    remove(id) {
      if (map.delete(id)) schedule();
    },
    clear() {
      if (map.size) {
        map.clear();
        schedule();
      }
    },
    entries: () => map.entries(),
    subscribe(listener) {
      listeners.add(listener);
      // Re-render now and then so cursors that stopped moving can fade out.
      idleTimer ??= setInterval(() => map.size && emit(), 2000);
      return () => {
        listeners.delete(listener);
        if (listeners.size === 0 && idleTimer) {
          clearInterval(idleTimer);
          idleTimer = undefined;
        }
      };
    },
    getVersion: () => version,
  };
}
