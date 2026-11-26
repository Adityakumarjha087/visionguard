"use client";
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { hasRole } from "@/lib/roles";
import { STORAGE_KEY, tokenStore } from "@/lib/token-store";
import type { Role, User } from "@/lib/types";

interface AuthContextValue {
  user: User | null;
  ready: boolean;
  login: (email: string, password: string) => Promise<User>;
  register: (email: string, password: string, name: string) => Promise<User>;
  logout: () => Promise<void>;
  can: (min: Role) => boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    setUser(tokenStore.get()?.user ?? null);
    setReady(true);
    const unsub = tokenStore.subscribe((a) => setUser(a?.user ?? null));
    const onStorage = (e: StorageEvent) => {
      if (e.key !== STORAGE_KEY) return;
      tokenStore.invalidate();
      setUser(tokenStore.get()?.user ?? null);
    };
    window.addEventListener("storage", onStorage);
    // refresh the cached profile (role may have changed) when a session exists
    if (tokenStore.accessToken()) {
      api
        .me()
        .then((u) => tokenStore.updateUser(u))
        .catch(() => undefined);
    }
    return () => {
      unsub();
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    const res = await api.login(email, password);
    return tokenStore.setFromResponse(res).user;
  }, []);

  const register = useCallback(async (email: string, password: string, name: string) => {
    const res = await api.register(email, password, name);
    return tokenStore.setFromResponse(res).user;
  }, []);

  const logout = useCallback(async () => {
    const rt = tokenStore.refreshToken();
    tokenStore.clear();
    if (rt) await api.logout(rt).catch(() => undefined);
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ user, ready, login, register, logout, can: (min) => hasRole(user?.role, min) }),
    [user, ready, login, register, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
