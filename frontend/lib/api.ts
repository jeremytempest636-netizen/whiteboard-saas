import { API_URL } from "./config";

export interface User {
  id: string;
  displayName: string;
}

export interface Session {
  token: string;
  /** Unix seconds. */
  expiresAt: number;
  user: User;
}

export interface Board {
  id: string;
  title: string;
  ownerId: string;
  createdAt: string;
  updatedAt: string;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init: RequestInit & { token?: string } = {}): Promise<T> {
  const { token, headers, ...rest } = init;
  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      ...rest,
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...headers,
      },
    });
  } catch {
    throw new ApiError(0, "network", "Can't reach the server. Check your connection and try again.");
  }
  const body = (await res.json().catch(() => null)) as
    | { error?: { code?: string; message?: string } }
    | T
    | null;
  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string } } | null)?.error;
    throw new ApiError(res.status, err?.code ?? "error", err?.message ?? "Something went wrong.");
  }
  return body as T;
}

export const api = {
  guest: (name: string) =>
    request<Session>("/api/auth/guest", { method: "POST", body: JSON.stringify({ name }) }),

  createBoard: (token: string, title: string) =>
    request<Board>("/api/boards", { method: "POST", token, body: JSON.stringify({ title }) }),

  listBoards: (token: string) =>
    request<{ boards: Board[] }>("/api/boards", { token }).then((r) => r.boards),

  getBoard: (token: string, id: string) => request<Board>(`/api/boards/${encodeURIComponent(id)}`, { token }),
};
