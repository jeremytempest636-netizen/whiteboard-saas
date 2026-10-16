/** Short, collision-resistant element id (96 random bits, URL safe). */
export function newId(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += b.toString(36).padStart(2, "0");
  return out;
}
