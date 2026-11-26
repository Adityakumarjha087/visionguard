export const API_URL = (process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:20901").replace(/\/+$/, "");

/** ws(s):// URL of the API WebSocket endpoint (token appended by the caller). */
export function wsUrl(token: string, base: string = API_URL): string {
  const ws = base.replace(/^http(s?):\/\//i, (_m, s: string) => `ws${s}://`);
  return `${ws}/api/v1/ws?access_token=${encodeURIComponent(token)}`;
}

/** Absolute media URL for a relative API path, with the access token for <img>/<video>. */
export function mediaUrl(path: string, token: string | null, base: string = API_URL): string {
  const abs = /^https?:\/\//i.test(path) ? path : `${base}${path.startsWith("/") ? "" : "/"}${path}`;
  if (!token) return abs;
  const sep = abs.includes("?") ? "&" : "?";
  return `${abs}${sep}access_token=${encodeURIComponent(token)}`;
}
