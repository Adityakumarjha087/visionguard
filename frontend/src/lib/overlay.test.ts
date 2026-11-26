import { describe, expect, it } from "vitest";
import {
  bboxToPixels,
  classColor,
  containRect,
  distanceToSegment,
  heatColor,
  heatmapCells,
  hexToRgba,
  normToPixel,
  pixelToNorm,
  pointInPolygon,
  polygonArea,
  trackLabel,
} from "./overlay";
import type { Point } from "./types";

describe("containRect (object-fit: contain)", () => {
  it("pillar-boxes a 16:9 frame in a square box", () => {
    expect(containRect(400, 400, 1600, 900)).toEqual({ x: 0, y: 87.5, width: 400, height: 225 });
  });
  it("letter-boxes a 4:3 frame in a wide box", () => {
    const r = containRect(800, 300, 640, 480);
    expect(r.height).toBe(300);
    expect(r.width).toBe(400);
    expect(r.x).toBe(200);
    expect(r.y).toBe(0);
  });
  it("falls back to the whole box when media size is unknown and to zero for empty boxes", () => {
    expect(containRect(320, 180, 0, 0)).toEqual({ x: 0, y: 0, width: 320, height: 180 });
    expect(containRect(0, 100, 16, 9)).toEqual({ x: 0, y: 0, width: 0, height: 0 });
  });
});

describe("normalised <-> pixel", () => {
  const r = containRect(400, 400, 1600, 900); // y offset 87.5
  it("maps normalised points into the content rect", () => {
    expect(normToPixel([0, 0], r)).toEqual([0, 87.5]);
    expect(normToPixel([1, 1], r)).toEqual([400, 312.5]);
    expect(normToPixel([0.5, 0.5], r)).toEqual([200, 200]);
  });
  it("round-trips pixel -> normalised", () => {
    const p = pixelToNorm(100, 143.75, r)!;
    expect(p[0]).toBeCloseTo(0.25);
    expect(p[1]).toBeCloseTo(0.25);
  });
  it("returns null in the letterbox bars unless clamped", () => {
    expect(pixelToNorm(200, 20, r)).toBeNull();
    expect(pixelToNorm(200, 20, r, true)).toEqual([0.5, 0]);
  });
  it("converts a normalised bbox (even when corners are swapped)", () => {
    const b = bboxToPixels([0.5, 0.5, 0.25, 0.25], { x: 0, y: 0, width: 200, height: 100 });
    expect(b).toEqual({ x: 50, y: 25, width: 50, height: 25 });
  });
});

describe("hit testing", () => {
  const square: Point[] = [
    [0, 0],
    [1, 0],
    [1, 1],
    [0, 1],
  ];
  const concave: Point[] = [
    [0, 0],
    [4, 0],
    [4, 4],
    [2, 2],
    [0, 4],
  ];
  it("detects points inside / outside a polygon", () => {
    expect(pointInPolygon([0.5, 0.5], square)).toBe(true);
    expect(pointInPolygon([1.5, 0.5], square)).toBe(false);
  });
  it("handles concave polygons and degenerate input", () => {
    expect(pointInPolygon([2, 3], concave)).toBe(false);
    expect(pointInPolygon([1, 1], concave)).toBe(true);
    expect(pointInPolygon([0.5, 0.5], square.slice(0, 2))).toBe(false);
  });
  it("measures distance to a segment and polygon area", () => {
    expect(distanceToSegment([0.5, 1], [0, 0], [1, 0])).toBeCloseTo(1);
    expect(distanceToSegment([2, 0], [0, 0], [1, 0])).toBeCloseTo(1);
    expect(polygonArea(square)).toBe(1);
  });
});

describe("colours and labels", () => {
  it("uses fixed colours for known classes and stable hashes for others", () => {
    expect(classColor("person")).toBe("#22c55e");
    expect(classColor("Person")).toBe("#22c55e");
    expect(classColor("forklift")).toBe(classColor("forklift"));
    expect(classColor("forklift")).toMatch(/^hsl\(/);
  });
  it("formats labels with track id and confidence", () => {
    expect(trackLabel({ id: 7, cls: "car", conf: 0.876, bbox: [0, 0, 1, 1] })).toBe("car #7 88%");
  });
  it("converts hex to rgba", () => {
    expect(hexToRgba("#ff0000", 0.5)).toBe("rgba(255, 0, 0, 0.5)");
    expect(hexToRgba("#0f0", 1)).toBe("rgba(0, 255, 0, 1)");
    expect(hexToRgba("not-a-colour", 1)).toBe("not-a-colour");
  });
  it("heat ramp is transparent at 0 and red at 1", () => {
    expect(heatColor(0)).toMatch(/, 0\)$/);
    expect(heatColor(1)).toBe("rgba(239, 68, 68, 0.65)");
    expect(heatColor(5)).toBe(heatColor(1));
  });
  it("iterates row-major heatmap cells, skipping zeros", () => {
    const cells = heatmapCells({ cols: 2, rows: 2, max: 4, cells: [0, 2, 4, 0] }, { x: 0, y: 0, width: 100, height: 50 });
    expect(cells).toHaveLength(2);
    expect(cells[0]!.rect).toEqual({ x: 50, y: 0, width: 50, height: 25 });
    expect(cells[0]!.t).toBe(0.5);
    expect(cells[1]!.rect).toEqual({ x: 0, y: 25, width: 50, height: 25 });
    expect(cells[1]!.t).toBe(1);
  });
});
