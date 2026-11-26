"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { drawHeatmap, fitCanvas } from "@/lib/canvas-draw";
import { containRect, heatColor } from "@/lib/overlay";
import type { Heatmap } from "@/lib/types";

export function HeatmapLegend({ max }: { max: number }) {
  const stops = Array.from({ length: 11 }, (_, i) => heatColor(i / 10, 1));
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground" aria-label="Heatmap legend">
      <span>0</span>
      <div className="h-2.5 w-48 rounded-full" style={{ background: `linear-gradient(to right, ${stops.join(", ")})` }} />
      <span>{max} detections</span>
    </div>
  );
}

/** Heatmap cells drawn on a canvas over the camera snapshot (object-fit: contain aware). */
export function HeatmapCanvas({ heatmap, imageSrc }: { heatmap: Heatmap; imageSrc: string | null }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const [imgOk, setImgOk] = useState(true);

  const draw = useCallback(() => {
    const c = canvasRef.current;
    if (!c) return;
    const { ctx, w, h } = fitCanvas(c);
    if (!ctx) return;
    const img = imgRef.current;
    const rect = containRect(w, h, img?.naturalWidth || 16, img?.naturalHeight || 9);
    drawHeatmap(ctx, heatmap, rect);
  }, [heatmap]);

  useEffect(() => {
    draw();
    const c = canvasRef.current;
    if (!c || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(draw);
    ro.observe(c);
    return () => ro.disconnect();
  }, [draw]);

  return (
    <div className="relative aspect-video overflow-hidden rounded-lg border bg-black">
      {imageSrc && imgOk ? (
        <img ref={imgRef} src={imageSrc} alt="Camera snapshot under heatmap" className="size-full object-contain opacity-80" onLoad={draw} onError={() => setImgOk(false)} />
      ) : null}
      <canvas ref={canvasRef} className="absolute inset-0 size-full" role="img" aria-label="Movement heatmap" data-testid="heatmap-canvas" />
    </div>
  );
}
