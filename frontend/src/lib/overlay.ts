/**
 * Pure geometry helpers for the canvas overlays (live boxes, zone editor, heatmap).
 * All API coordinates are normalised 0..1 relative to the video frame.
 */
import type { Point, Track } from "./types";

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * Where a `mediaW x mediaH` image is drawn inside a `boxW x boxH` element with `object-fit: contain`
 * (centred, letter/pillar-boxed). Falls back to the full box when media size is unknown.
 */
export function containRect(boxW: number, boxH: number, mediaW: number, mediaH: number): Rect {
  if (boxW <= 0 || boxH <= 0) return { x: 0, y: 0, width: 0, height: 0 };
  if (mediaW <= 0 || mediaH <= 0) return { x: 0, y: 0, width: boxW, height: boxH };
  const scale = Math.min(boxW / mediaW, boxH / mediaH);
  const width = mediaW * scale;
  const height = mediaH * scale;
  return { x: (boxW - width) / 2, y: (boxH - height) / 2, width, height };
}

export function normToPixel(p: Point, r: Rect): Point {
  return [r.x + p[0] * r.width, r.y + p[1] * r.height];
}

const clamp01 = (v: number) => Math.min(1, Math.max(0, v));

/** Pixel (relative to the element) -> normalised frame coords. Returns null outside the media area unless `clamp`. */
export function pixelToNorm(px: number, py: number, r: Rect, clamp = false): Point | null {
  if (r.width <= 0 || r.height <= 0) return null;
  const nx = (px - r.x) / r.width;
  const ny = (py - r.y) / r.height;
  if (!clamp && (nx < 0 || nx > 1 || ny < 0 || ny > 1)) return null;
  return [round4(clamp01(nx)), round4(clamp01(ny))];
}

export function round4(v: number): number {
  return Math.round(v * 10_000) / 10_000;
}

/** Normalised [x1,y1,x2,y2] -> pixel rect inside the content rect. */
export function bboxToPixels(b: Track["bbox"], r: Rect): Rect {
  const [x1, y1] = normToPixel([Math.min(b[0], b[2]), Math.min(b[1], b[3])], r);
  const [x2, y2] = normToPixel([Math.max(b[0], b[2]), Math.max(b[1], b[3])], r);
  return { x: x1, y: y1, width: x2 - x1, height: y2 - y1 };
}

/** Ray-casting point-in-polygon (works for any simple polygon, normalised or pixel coords). */
export function pointInPolygon(p: Point, poly: Point[]): boolean {
  if (poly.length < 3) return false;
  let inside = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [xi, yi] = poly[i]!;
    const [xj, yj] = poly[j]!;
    const intersect = yi > p[1] !== yj > p[1] && p[0] < ((xj - xi) * (p[1] - yi)) / (yj - yi) + xi;
    if (intersect) inside = !inside;
  }
  return inside;
}

/** Shortest distance from p to segment a-b. */
export function distanceToSegment(p: Point, a: Point, b: Point): number {
  const dx = b[0] - a[0];
  const dy = b[1] - a[1];
  const len2 = dx * dx + dy * dy;
  let t = len2 === 0 ? 0 : ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / len2;
  t = Math.max(0, Math.min(1, t));
  return Math.hypot(p[0] - (a[0] + t * dx), p[1] - (a[1] + t * dy));
}

/** Polygon area via the shoelace formula (absolute value). */
export function polygonArea(poly: Point[]): number {
  let s = 0;
  for (let i = 0; i < poly.length; i++) {
    const [x1, y1] = poly[i]!;
    const [x2, y2] = poly[(i + 1) % poly.length]!;
    s += x1 * y2 - x2 * y1;
  }
  return Math.abs(s) / 2;
}

export function centroid(poly: Point[]): Point {
  if (!poly.length) return [0, 0];
  const sx = poly.reduce((a, p) => a + p[0], 0);
  const sy = poly.reduce((a, p) => a + p[1], 0);
  return [sx / poly.length, sy / poly.length];
}

const CLASS_COLORS: Record<string, string> = {
  person: "#22c55e",
  car: "#3b82f6",
  truck: "#6366f1",
  bus: "#a855f7",
  motorcycle: "#06b6d4",
  bicycle: "#14b8a6",
  vehicle: "#3b82f6",
  helmet: "#eab308",
  no_helmet: "#ef4444",
  head: "#f97316",
  vest: "#84cc16",
};

/** Stable colour per class: known palette first, otherwise a hue hashed from the name. */
export function classColor(cls: string): string {
  const known = CLASS_COLORS[cls.toLowerCase()];
  if (known) return known;
  let h = 0;
  for (let i = 0; i < cls.length; i++) h = (h * 31 + cls.charCodeAt(i)) >>> 0;
  return `hsl(${h % 360} 80% 55%)`;
}

export function trackLabel(t: Track): string {
  return `${t.cls} #${t.id} ${Math.round(t.conf * 100)}%`;
}

export function hexToRgba(hex: string, alpha: number): string {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return hex;
  let h = m[1]!;
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  const n = parseInt(h, 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}

/** Heatmap colour ramp (transparent blue -> cyan -> green -> yellow -> red) for t in [0,1]. */
const RAMP: [number, [number, number, number]][] = [
  [0, [30, 64, 175]],
  [0.25, [6, 182, 212]],
  [0.5, [34, 197, 94]],
  [0.75, [234, 179, 8]],
  [1, [239, 68, 68]],
];

export function heatColor(t: number, maxAlpha = 0.65): string {
  const v = clamp01(Number.isFinite(t) ? t : 0);
  let i = 1;
  while (i < RAMP.length - 1 && v > RAMP[i]![0]) i++;
  const [t0, c0] = RAMP[i - 1]!;
  const [t1, c1] = RAMP[i]!;
  const f = t1 === t0 ? 0 : (v - t0) / (t1 - t0);
  const c = c0.map((a, k) => Math.round(a + (c1[k]! - a) * f));
  const alpha = v === 0 ? 0 : round4(0.15 + (maxAlpha - 0.15) * v);
  return `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${alpha})`;
}

/** Iterate row-major heatmap cells as pixel rects inside the content rect. */
export function heatmapCells(
  hm: { cols: number; rows: number; max: number; cells: number[] },
  r: Rect,
): { rect: Rect; value: number; t: number }[] {
  const out: { rect: Rect; value: number; t: number }[] = [];
  if (hm.cols <= 0 || hm.rows <= 0) return out;
  const cw = r.width / hm.cols;
  const ch = r.height / hm.rows;
  const max = hm.max > 0 ? hm.max : Math.max(0, ...hm.cells);
  for (let row = 0; row < hm.rows; row++) {
    for (let col = 0; col < hm.cols; col++) {
      const value = hm.cells[row * hm.cols + col] ?? 0;
      if (value <= 0) continue;
      out.push({
        rect: { x: r.x + col * cw, y: r.y + row * ch, width: cw, height: ch },
        value,
        t: max > 0 ? value / max : 0,
      });
    }
  }
  return out;
}
