import type { Camera, Incident, Zone } from "@/lib/types";

export const zone = (over: Partial<Zone> = {}): Zone => ({
  id: "z1",
  camera_id: "c1",
  name: "Restricted",
  kind: "polygon",
  points: [
    [0.1, 0.1],
    [0.5, 0.1],
    [0.5, 0.5],
  ],
  color: "#22c55e",
  created_at: "2026-11-26T10:00:00Z",
  ...over,
});

export const camera = (over: Partial<Camera> = {}): Camera => ({
  id: "c1",
  name: "Warehouse Floor (sample)",
  source_type: "file",
  source_uri: "samples/warehouse.mp4",
  enabled: true,
  privacy_mode: true,
  target_fps: 10,
  created_at: "2026-11-26T10:00:00Z",
  updated_at: "2026-11-26T10:00:00Z",
  status: { state: "online", fps: 9.8, latency_ms: 42 },
  ...over,
});

export const incident = (over: Partial<Incident> = {}): Incident => ({
  id: "i1",
  camera_id: "c1",
  camera_name: "Warehouse Floor (sample)",
  rule_id: "r1",
  rule_name: "Restricted area",
  rule_type: "zone_intrusion",
  severity: "high",
  status: "open",
  occurred_at: "2026-11-26T10:00:00Z",
  message: "person entered Restricted",
  object_class: "person",
  track_ids: [3],
  description: "A worker walks into the restricted zone near the racks.",
  description_provider: "mock",
  snapshot_url: "/api/v1/media/snapshots/i1.jpg",
  clip_url: null,
  acknowledged_by: null,
  updated_at: "2026-11-26T10:00:00Z",
  ...over,
});
