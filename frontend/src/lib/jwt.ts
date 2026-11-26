/** Decode a JWT payload without verifying it (client-side expiry hints only). */
export function decodeJwt(token: string): { exp?: number; sub?: string; role?: string } | null {
  const part = token.split(".")[1];
  if (!part) return null;
  try {
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
    return JSON.parse(atob(b64)) as { exp?: number };
  } catch {
    return null;
  }
}

/** True when the token expires within `skewSeconds` (or cannot be decoded). */
export function isExpiringSoon(token: string, skewSeconds = 30, now: number = Date.now()): boolean {
  const exp = decodeJwt(token)?.exp;
  if (typeof exp !== "number") return false;
  return exp * 1000 - now < skewSeconds * 1000;
}
