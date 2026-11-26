"use client";
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { wsUrl } from "@/lib/config";
import { isExpiringSoon } from "@/lib/jwt";
import { tokenStore } from "@/lib/token-store";
import type { WsMessage } from "@/lib/types";
import { ReconnectingWsClient, type WsStatus } from "@/lib/ws-client";
import { useAuth } from "./auth/auth-provider";

interface RealtimeValue {
  client: ReconnectingWsClient | null;
  status: WsStatus;
}

const RealtimeContext = createContext<RealtimeValue>({ client: null, status: "idle" });

async function freshUrl(): Promise<string | null> {
  let token = tokenStore.accessToken();
  if (!token) return null;
  if (isExpiringSoon(token)) {
    await api.refresh();
    token = tokenStore.accessToken();
    if (!token) return null;
  }
  return wsUrl(token);
}

export function RealtimeProvider({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const [client, setClient] = useState<ReconnectingWsClient | null>(null);
  const [status, setStatus] = useState<WsStatus>("idle");
  const userId = user?.id;
  const clientRef = useRef<ReconnectingWsClient | null>(null);

  useEffect(() => {
    if (!userId) return;
    const c = new ReconnectingWsClient({ url: freshUrl });
    clientRef.current = c;
    const off = c.onStatus(setStatus);
    c.start();
    setClient(c);
    return () => {
      off();
      c.stop();
      clientRef.current = null;
      setClient(null);
      setStatus("idle");
    };
  }, [userId]);

  return <RealtimeContext.Provider value={{ client, status }}>{children}</RealtimeContext.Provider>;
}

export function useRealtime(): RealtimeValue {
  return useContext(RealtimeContext);
}

/** Subscribe to one WS message type for the lifetime of the component. */
export function useWsEvent<T extends WsMessage["type"]>(type: T, handler: (msg: Extract<WsMessage, { type: T }>) => void) {
  const { client } = useRealtime();
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => {
    if (!client) return;
    return client.on(type, (m) => ref.current(m));
  }, [client, type]);
}

/** Subscribe to live frames of a camera (sends {"type":"subscribe"}). */
export function useCameraSubscription(cameraId: string | undefined) {
  const { client } = useRealtime();
  useEffect(() => {
    if (!client || !cameraId) return;
    return client.subscribe(cameraId);
  }, [client, cameraId]);
}
