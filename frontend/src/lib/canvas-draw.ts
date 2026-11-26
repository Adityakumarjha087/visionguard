import {
  bboxToPixels,
  centroid,
  classColor,
  containRect,
  heatColor,
  heatmapCells,
  hexToRgba,
  normToPixel,
  trackLabel,
  type Rect,
} from "./overlay";
import type { Heatmap, Point, Track, Zone } from "./types";

/** Resize the canvas backing store to its CSS size * devicePixelRatio. Returns CSS size. */
export function fitCanvas(canvas: HTMLCanvasElement): { w: number; h: number; ctx: CanvasRenderingContext2D | null } {
  const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  const ctx = canvas.getContext("2d");
  ctx?.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx?.clearRect(0, 0, w, h);
  return { w, h, ctx };
}

export function contentRectFor(canvas: HTMLCanvasElement, mediaW: number, mediaH: number): Rect {
  return containRect(canvas.clientWidth, canvas.clientHeight, mediaW, mediaH);
}

export function drawZones(ctx: CanvasRenderingContext2D, zones: Pick<Zone, "kind" | "points" | "color" | "name">[], r: Rect) {
  for (const z of zones) drawShape(ctx, z.points, z.kind, z.color, r, z.name);
}

export function drawShape(
  ctx: CanvasRenderingContext2D,
  points: Point[],
  kind: "polygon" | "line",
  color: string,
  r: Rect,
  label?: string,
  opts: { closed?: boolean; vertices?: boolean } = {},
) {
  if (!points.length) return;
  const px = points.map((p) => normToPixel(p, r));
  const closed = opts.closed ?? kind === "polygon";
  ctx.save();
  ctx.lineWidth = 2;
  ctx.strokeStyle = color;
  ctx.fillStyle = hexToRgba(color, 0.2);
  ctx.beginPath();
  px.forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)));
  if (closed && kind === "polygon" && px.length >= 3) {
    ctx.closePath();
    ctx.fill();
  }
  ctx.stroke();
  if (kind === "line" && px.length === 2) {
    // direction arrow at the midpoint: "in" = left side of p0->p1
    const [a, b] = px as [Point, Point];
    const mx = (a[0] + b[0]) / 2;
    const my = (a[1] + b[1]) / 2;
    const ang = Math.atan2(b[1] - a[1], b[0] - a[0]) - Math.PI / 2;
    ctx.beginPath();
    ctx.moveTo(mx, my);
    ctx.lineTo(mx + Math.cos(ang) * 18, my + Math.sin(ang) * 18);
    ctx.stroke();
  }
  if (opts.vertices) {
    ctx.fillStyle = color;
    for (const [x, y] of px) {
      ctx.beginPath();
      ctx.arc(x, y, 4, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  if (label) {
    const [cx, cy] = normToPixel(centroid(points), r);
    ctx.font = "600 12px ui-sans-serif, system-ui, sans-serif";
    ctx.fillStyle = color;
    ctx.textAlign = "center";
    ctx.fillText(label, cx, cy);
  }
  ctx.restore();
}

export function drawTracks(ctx: CanvasRenderingContext2D, tracks: Track[], r: Rect) {
  ctx.save();
  ctx.font = "600 12px ui-sans-serif, system-ui, sans-serif";
  ctx.textBaseline = "top";
  for (const t of tracks) {
    const b = bboxToPixels(t.bbox, r);
    const color = classColor(t.cls);
    ctx.lineWidth = 2;
    ctx.strokeStyle = color;
    ctx.strokeRect(b.x, b.y, b.width, b.height);
    const label = trackLabel(t);
    const tw = ctx.measureText(label).width + 8;
    const ly = b.y - 18 < r.y ? b.y : b.y - 18;
    ctx.fillStyle = color;
    ctx.fillRect(b.x, ly, tw, 18);
    ctx.fillStyle = "#0b0b0b";
    ctx.fillText(label, b.x + 4, ly + 3);
  }
  ctx.restore();
}

export function drawHeatmap(ctx: CanvasRenderingContext2D, hm: Heatmap, r: Rect) {
  for (const c of heatmapCells(hm, r)) {
    ctx.fillStyle = heatColor(c.t);
    ctx.fillRect(c.rect.x, c.rect.y, Math.ceil(c.rect.width), Math.ceil(c.rect.height));
  }
}
