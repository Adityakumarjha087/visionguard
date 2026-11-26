"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { Activity, Gauge, Layers } from "lucide-react";
import { LiveStream } from "@/components/cameras/live-stream";
import { useCameraSubscription, useWsEvent } from "@/components/realtime-provider";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { contentRectFor, drawTracks, drawZones, fitCanvas } from "@/lib/canvas-draw";
import { classColor } from "@/lib/overlay";
import { FpsMeter } from "@/lib/fps-meter";
import type { Camera, FrameMsg, Zone } from "@/lib/types";

const STALE_MS = 3_000;

export function LiveOverlay({ camera, zones }: { camera: Camera; zones: Zone[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const frameRef = useRef<FrameMsg | null>(null);
  const lastAt = useRef(0);
  const meter = useRef(new FpsMeter());
  const [stats, setStats] = useState({ fps: 0, latency: 0, tracks: 0, frame: 0 });
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [showZones, setShowZones] = useState(true);

  useCameraSubscription(camera.id);

  const draw = useCallback(() => {
    const c = canvasRef.current;
    if (!c) return;
    const { ctx } = fitCanvas(c);
    if (!ctx) return;
    const f = frameRef.current;
    const img = imgRef.current;
    const mw = f?.width || img?.naturalWidth || 16;
    const mh = f?.height || img?.naturalHeight || 9;
    const rect = contentRectFor(c, mw, mh);
    if (showZones) drawZones(ctx, zones, rect);
    if (f && performance.now() - lastAt.current < STALE_MS) drawTracks(ctx, f.tracks, rect);
  }, [zones, showZones]);

  useWsEvent("frame", ({ data }) => {
    if (data.camera_id !== camera.id) return;
    frameRef.current = data;
    const now = performance.now();
    lastAt.current = now;
    const measured = meter.current.tick(now);
    const by: Record<string, number> = {};
    for (const t of data.tracks) by[t.cls] = (by[t.cls] ?? 0) + 1;
    setCounts(by);
    setStats({
      fps: data.fps > 0 ? data.fps : measured,
      latency: data.latency_ms,
      tracks: data.tracks.length,
      frame: data.frame,
    });
    requestAnimationFrame(draw);
  });

  // redraw on resize / zone toggle, and clear stale boxes
  useEffect(() => {
    draw();
    const c = canvasRef.current;
    const ro = typeof ResizeObserver !== "undefined" && c ? new ResizeObserver(() => draw()) : null;
    if (c) ro?.observe(c);
    const t = setInterval(() => {
      if (lastAt.current && performance.now() - lastAt.current > STALE_MS) {
        draw();
        setStats((s) => (s.fps === 0 ? s : { ...s, fps: 0, tracks: 0 }));
      }
    }, 1_000);
    return () => {
      ro?.disconnect();
      clearInterval(t);
    };
  }, [draw]);

  return (
    <div className="space-y-3">
      <div className="relative aspect-video overflow-hidden rounded-xl border bg-black">
        <LiveStream ref={imgRef} cameraId={camera.id} name={camera.name} disabled={!camera.enabled} className="absolute inset-0" onLoad={draw} />
        <canvas ref={canvasRef} className="pointer-events-none absolute inset-0 size-full" data-testid="live-canvas" aria-hidden />
        <div className="absolute left-3 top-3 flex flex-wrap gap-2 font-mono text-xs">
          <span className="inline-flex items-center gap-1 rounded-md bg-black/70 px-2 py-1 text-emerald-400" aria-label="Frames per second">
            <Activity className="size-3" />
            <span data-testid="fps-counter">{stats.fps.toFixed(1)}</span> FPS
          </span>
          <span className="inline-flex items-center gap-1 rounded-md bg-black/70 px-2 py-1 text-sky-300" aria-label="Inference latency">
            <Gauge className="size-3" />
            <span data-testid="latency-counter">{stats.latency.toFixed(0)}</span> ms
          </span>
          <span className="rounded-md bg-black/70 px-2 py-1 text-white/80">{stats.tracks} objects</span>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {Object.entries(counts).map(([cls, n]) => (
          <Badge key={cls} variant="outline" className="gap-1.5">
            <span className="size-2 rounded-full" style={{ background: classColor(cls) }} aria-hidden />
            {cls}: {n}
          </Badge>
        ))}
        <div className="ml-auto flex items-center gap-2">
          <Layers className="size-4 text-muted-foreground" aria-hidden />
          <Label htmlFor="show-zones" className="text-sm">
            Show zones
          </Label>
          <Switch id="show-zones" checked={showZones} onCheckedChange={setShowZones} />
        </div>
      </div>
    </div>
  );
}
