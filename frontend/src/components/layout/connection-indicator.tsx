"use client";
import { useRealtime } from "@/components/realtime-provider";
import type { WsStatus } from "@/lib/ws-client";
import { cn } from "@/lib/utils";

const LABEL: Record<WsStatus, string> = {
  idle: "Offline",
  connecting: "Connecting",
  open: "Live",
  closed: "Reconnecting",
};

export function ConnectionDot({ status }: { status: WsStatus }) {
  return (
    <span
      role="status"
      aria-label={`Realtime connection: ${LABEL[status]}`}
      data-testid="ws-status"
      data-status={status}
      className="inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium"
    >
      <span
        aria-hidden
        className={cn(
          "size-2 rounded-full",
          status === "open" ? "bg-emerald-500" : status === "connecting" ? "animate-pulse bg-amber-500" : "bg-red-500",
        )}
      />
      <span className="hidden sm:inline">{LABEL[status]}</span>
    </span>
  );
}

export function ConnectionIndicator() {
  const { status } = useRealtime();
  return <ConnectionDot status={status} />;
}
