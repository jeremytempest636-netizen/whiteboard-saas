/**
 * Public endpoints. NEXT_PUBLIC_* values are inlined at build time, so the
 * Docker build receives them as build args.
 */
const api = (process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

const ws = (
  process.env.NEXT_PUBLIC_WS_URL ?? api.replace(/^http/, "ws")
).replace(/\/$/, "");

export const API_URL = api;
export const WS_URL = ws;
