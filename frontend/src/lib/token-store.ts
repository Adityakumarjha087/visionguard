import type { AuthResponse, User } from "./types";

/**
 * Token persistence.
 *
 * Access + refresh tokens are kept in localStorage (key `visionguard.auth`). This is a deliberate
 * trade-off for a demo dashboard: it survives reloads and lets <img>/WebSocket URLs carry the access
 * token as `?access_token=`. The access token is short lived (15 min) and the refresh token is
 * rotated on each refresh. A hardened deployment would move the refresh token to an httpOnly cookie.
 */
export interface StoredAuth {
  access_token: string;
  refresh_token: string;
  user: User;
}

type Listener = (auth: StoredAuth | null) => void;

export const STORAGE_KEY = "visionguard.auth";

export class TokenStore {
  private listeners = new Set<Listener>();
  private cache: StoredAuth | null | undefined;

  constructor(private readonly storage: () => Storage | null = defaultStorage) {}

  get(): StoredAuth | null {
    if (this.cache !== undefined) return this.cache;
    const s = this.storage();
    if (!s) return null;
    try {
      const raw = s.getItem(STORAGE_KEY);
      const parsed = raw ? (JSON.parse(raw) as StoredAuth) : null;
      this.cache = parsed && parsed.access_token && parsed.refresh_token ? parsed : null;
    } catch {
      this.cache = null;
    }
    return this.cache;
  }

  accessToken(): string | null {
    return this.get()?.access_token ?? null;
  }

  refreshToken(): string | null {
    return this.get()?.refresh_token ?? null;
  }

  setFromResponse(res: AuthResponse): StoredAuth {
    const auth: StoredAuth = { access_token: res.access_token, refresh_token: res.refresh_token, user: res.user };
    this.set(auth);
    return auth;
  }

  set(auth: StoredAuth | null): void {
    this.cache = auth;
    const s = this.storage();
    try {
      if (s) {
        if (auth) s.setItem(STORAGE_KEY, JSON.stringify(auth));
        else s.removeItem(STORAGE_KEY);
      }
    } catch {
      /* storage unavailable (private mode) - keep in memory */
    }
    this.listeners.forEach((l) => l(auth));
  }

  updateUser(user: User): void {
    const cur = this.get();
    if (cur) this.set({ ...cur, user });
  }

  clear(): void {
    this.set(null);
  }

  /** Drop the in-memory cache (e.g. another tab changed storage). */
  invalidate(): void {
    this.cache = undefined;
  }

  subscribe(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }
}

function defaultStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export const tokenStore = new TokenStore();
