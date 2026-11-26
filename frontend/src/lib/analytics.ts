import type { Bucket, CountPoint, LineCrossingPoint } from "./types";
import { VEHICLE_CLASSES } from "./rules";

export type RangeKey = "1h" | "6h" | "24h";

export const RANGES: Record<RangeKey, { ms: number; bucket: Bucket; crossBucket: Bucket; label: string }> = {
  "1h": { ms: 3_600_000, bucket: "1m", crossBucket: "5m", label: "Last hour" },
  "6h": { ms: 6 * 3_600_000, bucket: "5m", crossBucket: "15m", label: "Last 6 hours" },
  "24h": { ms: 24 * 3_600_000, bucket: "15m", crossBucket: "1h", label: "Last 24 hours" },
};

export function rangeParams(key: RangeKey, now: number = Date.now()) {
  const r = RANGES[key];
  return { from: new Date(now - r.ms).toISOString(), to: new Date(now).toISOString(), bucket: r.bucket, crossBucket: r.crossBucket };
}

export interface CountRow {
  bucket: string;
  t: number;
  people: number;
  vehicles: number;
}

/** Pivot per-class rows into people / vehicles per bucket (avg counts summed across vehicle classes). */
export function pivotCounts(points: CountPoint[]): CountRow[] {
  const by = new Map<string, CountRow>();
  for (const p of points) {
    let row = by.get(p.bucket);
    if (!row) {
      row = { bucket: p.bucket, t: new Date(p.bucket).getTime(), people: 0, vehicles: 0 };
      by.set(p.bucket, row);
    }
    const cls = p.object_class.toLowerCase();
    if (cls === "person") row.people += p.avg_count;
    else if (VEHICLE_CLASSES.includes(cls)) row.vehicles += p.avg_count;
  }
  return [...by.values()]
    .sort((a, b) => a.t - b.t)
    .map((r) => ({ ...r, people: round2(r.people), vehicles: round2(r.vehicles) }));
}

export interface CrossingRow {
  bucket: string;
  t: number;
  in: number;
  out: number;
}

export function pivotCrossings(points: LineCrossingPoint[], zoneId?: string): CrossingRow[] {
  const by = new Map<string, CrossingRow>();
  for (const p of points) {
    if (zoneId && p.zone_id !== zoneId) continue;
    let row = by.get(p.bucket);
    if (!row) {
      row = { bucket: p.bucket, t: new Date(p.bucket).getTime(), in: 0, out: 0 };
      by.set(p.bucket, row);
    }
    row[p.direction] += p.count;
  }
  return [...by.values()].sort((a, b) => a.t - b.t);
}

export function crossingTotals(points: LineCrossingPoint[]) {
  return points.reduce(
    (acc, p) => {
      acc[p.direction] += p.count;
      return acc;
    },
    { in: 0, out: 0 },
  );
}

function round2(n: number) {
  return Math.round(n * 100) / 100;
}
