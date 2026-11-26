"use client";
import { forwardRef, useEffect, useRef, useState } from "react";
import { VideoOff } from "lucide-react";
import { api } from "@/lib/api";
import { mediaUrl } from "@/lib/config";
import { isExpiringSoon } from "@/lib/jwt";
import { tokenStore } from "@/lib/token-store";
import { cn } from "@/lib/utils";

export interface LiveStreamProps {
  cameraId: string;
  name: string;
  className?: string;
  imgClassName?: string;
  disabled?: boolean;
  onLoad?: (img: HTMLImageElement) => void;
}

/**
 * MJPEG live view (<img src=".../stream?access_token=...">). The URL is fixed while the stream is open
 * (so token refreshes don't restart it); on error it refreshes the token if needed and reconnects.
 */
export const LiveStream = forwardRef<HTMLImageElement, LiveStreamProps>(function LiveStream(
  { cameraId, name, className, imgClassName, disabled, onLoad },
  ref,
) {
  const [src, setSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const retries = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const t = tokenStore.accessToken();
    setSrc(t && !disabled ? `${mediaUrl(`/api/v1/cameras/${cameraId}/stream`, t)}&_r=0` : null);
    setFailed(false);
    retries.current = 0;
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [cameraId, disabled]);

  function handleError() {
    setFailed(true);
    const attempt = ++retries.current;
    const delay = Math.min(15_000, 1_000 * 2 ** Math.min(attempt, 4));
    timer.current = setTimeout(async () => {
      let t = tokenStore.accessToken();
      if (t && isExpiringSoon(t)) {
        await api.refresh();
        t = tokenStore.accessToken();
      }
      if (t) setSrc(`${mediaUrl(`/api/v1/cameras/${cameraId}/stream`, t)}&_r=${attempt}`);
    }, delay);
  }

  return (
    <div className={cn("relative overflow-hidden bg-black", className)}>
      {src ? (
        <img
          ref={ref}
          src={src}
          alt={`Live view of ${name}`}
          className={cn("size-full object-contain", failed && "invisible", imgClassName)}
          onLoad={(e) => {
            setFailed(false);
            retries.current = 0;
            onLoad?.(e.currentTarget);
          }}
          onError={handleError}
        />
      ) : null}
      {(failed || !src) && (
        <div className="absolute inset-0 grid place-items-center text-xs text-white/70">
          <span className="flex flex-col items-center gap-2">
            <VideoOff className="size-6" />
            {disabled ? "Camera disabled" : "Waiting for stream…"}
          </span>
        </div>
      )}
    </div>
  );
});
