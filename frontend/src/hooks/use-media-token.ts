"use client";
import { useEffect, useState } from "react";
import { tokenStore } from "@/lib/token-store";
import { mediaUrl } from "@/lib/config";

/** Current access token (updates after refresh) for building <img>/<video> URLs. */
export function useAccessToken(): string | null {
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    setToken(tokenStore.accessToken());
    return tokenStore.subscribe((a) => setToken(a?.access_token ?? null));
  }, []);
  return token;
}

/** Absolute media URL with ?access_token= (null while the token is unknown or path is null). */
export function useMediaUrl(path: string | null | undefined): string | null {
  const token = useAccessToken();
  if (!path || !token) return null;
  return mediaUrl(path, token);
}
