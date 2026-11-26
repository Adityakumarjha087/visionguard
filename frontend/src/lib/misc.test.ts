import { describe, expect, it } from "vitest";
import { pivotCounts, pivotCrossings, crossingTotals, rangeParams } from "./analytics";
import { mediaUrl, wsUrl } from "./config";
import { decodeJwt, isExpiringSoon } from "./jwt";
import { hasRole } from "./roles";
import { defaultParams, parseClasses, sanitizeParams, validateRule, zonesForType } from "./rules";
import { canFinish, initialDraft, zoneDraftReducer } from "./zone-draft";
import { zone } from "@/test/fixtures";
import { FpsMeter } from "./fps-meter";

describe("config urls", () => {
  it("derives ws URL from the API base", () => {
    expect(wsUrl("a b", "http://localhost:20901")).toBe("ws://localhost:20901/api/v1/ws?access_token=a%20b");
    expect(wsUrl("t", "https://vg.example.com")).toBe("wss://vg.example.com/api/v1/ws?access_token=t");
  });
  it("prefixes relative media paths and appends the token", () => {
    expect(mediaUrl("/api/v1/media/clips/x.mp4", "tok", "http://api")).toBe("http://api/api/v1/media/clips/x.mp4?access_token=tok");
    expect(mediaUrl("/a?b=1", "tok", "http://api")).toBe("http://api/a?b=1&access_token=tok");
    expect(mediaUrl("/a", null, "http://api")).toBe("http://api/a");
  });
});

describe("roles", () => {
  it("orders admin > operator > viewer", () => {
    expect(hasRole("admin", "operator")).toBe(true);
    expect(hasRole("operator", "operator")).toBe(true);
    expect(hasRole("viewer", "operator")).toBe(false);
    expect(hasRole(undefined, "viewer")).toBe(false);
  });
});

describe("jwt", () => {
  const tok = (exp: number) => `x.${btoa(JSON.stringify({ exp })).replace(/=+$/, "")}.y`;
  it("decodes and detects expiry", () => {
    expect(decodeJwt(tok(123))?.exp).toBe(123);
    expect(decodeJwt("garbage")).toBeNull();
    expect(isExpiringSoon(tok(100), 30, 90_000)).toBe(true);
    expect(isExpiringSoon(tok(1000), 30, 90_000)).toBe(false);
  });
});

describe("rules helpers", () => {
  it("has type-specific defaults", () => {
    expect(defaultParams("dwell_time")).toEqual({ seconds: 120, classes: ["car", "truck", "bus", "motorcycle", "vehicle"], cooldown_seconds: 30 });
    expect(defaultParams("crowd_count").max_count).toBe(5);
  });
  it("filters zones by required kind and sanitises params", () => {
    const zs = [zone(), zone({ id: "l1", kind: "line", points: [[0, 0], [1, 1]] })];
    expect(zonesForType("line_crossing", zs).map((z) => z.id)).toEqual(["l1"]);
    expect(zonesForType("zone_intrusion", zs).map((z) => z.id)).toEqual(["z1"]);
    expect(sanitizeParams("crowd_count", { max_count: 3, seconds: 9, classes: ["person"] })).toEqual({ max_count: 3 });
    expect(parseClasses("Person, car  car,")).toEqual(["person", "car"]);
  });
  it("validates required zones", () => {
    const base = { name: "r", type: "line_crossing" as const, zone_id: "z1", params: {}, severity: "low" as const, enabled: true, notify_email: false, notify_telegram: false };
    expect(validateRule(base, [zone()]).zone_id).toMatch(/line/);
    expect(validateRule({ ...base, type: "crowd_count", zone_id: null, params: { max_count: 2 } }, [])).toEqual({});
  });
});

describe("zone draft reducer", () => {
  it("adds polygon points, ignores duplicates and finishes at >= 3", () => {
    let d = initialDraft("polygon");
    d = zoneDraftReducer(d, { type: "add", point: [0.1, 0.1] });
    d = zoneDraftReducer(d, { type: "add", point: [0.1, 0.1] });
    d = zoneDraftReducer(d, { type: "add", point: [0.5, 0.1] });
    expect(d.points).toHaveLength(2);
    expect(zoneDraftReducer(d, { type: "finish" }).closed).toBe(false);
    d = zoneDraftReducer(d, { type: "add", point: [0.5, 0.5] });
    d = zoneDraftReducer(d, { type: "finish" });
    expect(d.closed).toBe(true);
    expect(zoneDraftReducer(d, { type: "add", point: [0.9, 0.9] }).points).toHaveLength(3);
    expect(zoneDraftReducer(d, { type: "undo" })).toMatchObject({ closed: false, points: [[0.1, 0.1], [0.5, 0.1]] });
  });
  it("closes a line after exactly two points and resets on kind change", () => {
    let d = initialDraft("line");
    d = zoneDraftReducer(d, { type: "add", point: [0, 0] });
    d = zoneDraftReducer(d, { type: "add", point: [1, 1] });
    expect(d.closed).toBe(true);
    expect(canFinish(d)).toBe(true);
    expect(zoneDraftReducer(d, { type: "kind", kind: "polygon" })).toEqual(initialDraft("polygon"));
  });
});

describe("analytics pivots", () => {
  it("sums vehicle classes and sorts by time", () => {
    const rows = pivotCounts([
      { bucket: "2026-11-26T10:01:00Z", object_class: "person", avg_count: 2, max_count: 3 },
      { bucket: "2026-11-26T10:00:00Z", object_class: "car", avg_count: 1.5, max_count: 2 },
      { bucket: "2026-11-26T10:00:00Z", object_class: "truck", avg_count: 1, max_count: 1 },
      { bucket: "2026-11-26T10:00:00Z", object_class: "helmet", avg_count: 4, max_count: 4 },
    ]);
    expect(rows.map((r) => [r.people, r.vehicles])).toEqual([
      [0, 2.5],
      [2, 0],
    ]);
  });
  it("groups crossings by bucket and direction", () => {
    const pts = [
      { bucket: "2026-11-26T10:00:00Z", zone_id: "l", zone_name: "Gate", direction: "in" as const, count: 3 },
      { bucket: "2026-11-26T10:00:00Z", zone_id: "l", zone_name: "Gate", direction: "out" as const, count: 1 },
      { bucket: "2026-11-26T10:05:00Z", zone_id: "m", zone_name: "Door", direction: "in" as const, count: 2 },
    ];
    expect(pivotCrossings(pts)).toMatchObject([
      { in: 3, out: 1 },
      { in: 2, out: 0 },
    ]);
    expect(pivotCrossings(pts, "m")).toHaveLength(1);
    expect(crossingTotals(pts)).toEqual({ in: 5, out: 1 });
  });
  it("computes time ranges", () => {
    const p = rangeParams("6h", Date.parse("2026-11-26T12:00:00Z"));
    expect(p).toMatchObject({ from: "2026-11-26T06:00:00.000Z", to: "2026-11-26T12:00:00.000Z", bucket: "5m" });
  });
});

describe("FpsMeter", () => {
  it("measures arrival rate over a window", () => {
    const m = new FpsMeter(2000);
    for (let i = 0; i <= 10; i++) m.tick(i * 100);
    expect(m.value(1000)).toBeCloseTo(10);
    expect(m.value(10_000)).toBe(0);
  });
});
