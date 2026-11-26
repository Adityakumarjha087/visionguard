"use client";
import { useCallback } from "react";
import { useWsEvent } from "@/components/realtime-provider";
import { api } from "@/lib/api";
import type { Camera } from "@/lib/types";
import { useAsync } from "./use-async";

/** Camera list kept fresh with WS `camera_status` broadcasts. */
export function useCameras() {
  const state = useAsync(() => api.listCameras(), []);
  const { setData } = state;
  const onStatus = useCallback(
    (msg: { data: { camera_id: string } & Camera["status"] }) => {
      const { camera_id, ...status } = msg.data;
      setData((prev) => prev?.map((c) => (c.id === camera_id ? { ...c, status: { ...c.status, ...status } } : c)));
    },
    [setData],
  );
  useWsEvent("camera_status", onStatus);
  return state;
}
