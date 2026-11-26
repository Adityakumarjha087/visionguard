import type { Point, ZoneKind } from "./types";

export interface ZoneDraft {
  kind: ZoneKind;
  points: Point[];
  closed: boolean;
}

export type ZoneDraftAction =
  | { type: "add"; point: Point }
  | { type: "undo" }
  | { type: "clear" }
  | { type: "finish" }
  | { type: "kind"; kind: ZoneKind };

export const initialDraft = (kind: ZoneKind = "polygon"): ZoneDraft => ({ kind, points: [], closed: false });

export function minPoints(kind: ZoneKind): number {
  return kind === "line" ? 2 : 3;
}

export function canFinish(d: ZoneDraft): boolean {
  return d.kind === "line" ? d.points.length === 2 : d.points.length >= 3;
}

/** Drawing state machine for the zone editor (pure; unit tested). */
export function zoneDraftReducer(d: ZoneDraft, a: ZoneDraftAction): ZoneDraft {
  switch (a.type) {
    case "add": {
      if (d.closed) return d;
      const last = d.points[d.points.length - 1];
      if (last && Math.abs(last[0] - a.point[0]) < 1e-3 && Math.abs(last[1] - a.point[1]) < 1e-3) return d;
      const points = [...d.points, a.point];
      // a line is complete as soon as it has two points
      if (d.kind === "line" && points.length >= 2) return { ...d, points: points.slice(0, 2), closed: true };
      return { ...d, points };
    }
    case "undo":
      return { ...d, points: d.points.slice(0, -1), closed: false };
    case "clear":
      return { ...d, points: [], closed: false };
    case "finish":
      return canFinish(d) ? { ...d, closed: true } : d;
    case "kind":
      return a.kind === d.kind ? d : initialDraft(a.kind);
    default:
      return d;
  }
}
