import type { Session } from "./api";

const KEY = "whiteboard.session.v1";

/** Reads the saved session; returns null when absent, corrupt or expired. */
export function loadSession(): Session | null {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as Session;
    if (!s.token || !s.user?.id || s.expiresAt * 1000 <= Date.now() + 60_000) return null;
    return s;
  } catch {
    return null;
  }
}

export function saveSession(s: Session): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* storage unavailable (private mode): the session lives for this tab only */
  }
}

export function clearSession(): void {
  try {
    window.localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

/** Accepts a full board link or a bare board code and returns the code. */
export function parseBoardCode(input: string): string | null {
  const s = input.trim();
  if (!s) return null;
  const fromUrl = /\/board\/([a-z0-9]{6,16})\/?(?:[?#].*)?$/i.exec(s);
  const code = fromUrl?.[1] ?? s;
  return /^[a-z0-9]{6,16}$/i.test(code) ? code.toLowerCase() : null;
}
