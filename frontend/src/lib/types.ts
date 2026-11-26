// Types mirrored from docs/API.md. Keep in sync with the Go API contract.

export type Role = "admin" | "operator" | "viewer";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  created_at: string;
}

export interface AuthResponse {
  access_token: string;
  refresh_token: string;
  token_type: "Bearer";
  expires_in: number;
  user: User;
}

export interface ApiErrorBody {
  error: { code: ApiErrorCode; message: string; details?: Record<string, string> };
}

export type ApiErrorCode =
  | "bad_request"
  | "validation_error"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "rate_limited"
  | "internal";

// ---------- Cameras ----------
export type SourceType = "rtsp" | "webcam" | "file";
export type CameraState = "starting" | "online" | "offline" | "error" | "disabled";

export interface CameraStatus {
  state: CameraState;
  fps: number;
  latency_ms: number;
  message?: string;
  updated_at?: string;
}

export interface Camera {
  id: string;
  name: string;
  source_type: SourceType;
  source_uri: string;
  enabled: boolean;
  privacy_mode: boolean;
  target_fps: number;
  created_at: string;
  updated_at: string;
  status: CameraStatus;
}

export interface CameraCreate {
  name: string;
  source_type: SourceType;
  source_uri: string;
  enabled?: boolean;
  privacy_mode?: boolean;
  target_fps?: number;
}

export interface CameraPatch {
  name?: string;
  source_uri?: string;
  enabled?: boolean;
  privacy_mode?: boolean;
  target_fps?: number;
}

export interface Sample {
  name: string;
  source_uri: string;
  size_bytes: number;
}

// ---------- Zones ----------
export type ZoneKind = "polygon" | "line";
export type Point = [number, number];

export interface Zone {
  id: string;
  camera_id: string;
  name: string;
  kind: ZoneKind;
  points: Point[];
  color: string;
  created_at: string;
}

export interface ZoneInput {
  name: string;
  kind: ZoneKind;
  points: Point[];
  color?: string;
}

// ---------- Rules ----------
export type RuleType = "zone_intrusion" | "dwell_time" | "missing_ppe" | "crowd_count" | "line_crossing";
export type Severity = "low" | "medium" | "high" | "critical";

export interface RuleParams {
  classes?: string[];
  seconds?: number;
  max_count?: number;
  cooldown_seconds?: number;
}

export interface Rule {
  id: string;
  camera_id: string;
  zone_id: string | null;
  name: string;
  type: RuleType;
  params: RuleParams;
  severity: Severity;
  enabled: boolean;
  notify_email: boolean;
  notify_telegram: boolean;
  created_at: string;
}

export interface RuleInput {
  name: string;
  type: RuleType;
  zone_id?: string | null;
  params?: RuleParams;
  severity?: Severity;
  enabled?: boolean;
  notify_email?: boolean;
  notify_telegram?: boolean;
}

// ---------- Incidents ----------
export type IncidentStatus = "open" | "acknowledged" | "resolved";

export interface Incident {
  id: string;
  camera_id: string;
  camera_name: string;
  rule_id: string | null;
  rule_name: string;
  rule_type: RuleType;
  severity: Severity;
  status: IncidentStatus;
  occurred_at: string;
  message: string;
  object_class: string;
  track_ids: number[];
  description: string | null;
  description_provider: string | null;
  snapshot_url: string | null;
  clip_url: string | null;
  acknowledged_by: string | null;
  updated_at: string;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface IncidentFilters {
  q?: string;
  camera_id?: string;
  severity?: Severity | "";
  status?: IncidentStatus | "";
  rule_type?: RuleType | "";
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}

// ---------- Analytics ----------
export interface AnalyticsSummary {
  cameras_online: number;
  cameras_total: number;
  open_incidents: number;
  incidents_24h: number;
  people_now: number;
  vehicles_now: number;
  crossings_in_24h: number;
  crossings_out_24h: number;
}

export type Bucket = "10s" | "1m" | "5m" | "15m" | "1h";

export interface CountPoint {
  bucket: string;
  object_class: string;
  avg_count: number;
  max_count: number;
}

export interface Heatmap {
  cols: number;
  rows: number;
  max: number;
  cells: number[];
}

export interface LineCrossingPoint {
  bucket: string;
  zone_id: string;
  zone_name: string;
  direction: "in" | "out";
  count: number;
}

// ---------- Models ----------
export type ModelBackend = "color" | "motion" | "pytorch" | "onnx";

export interface Model {
  id: string;
  name: string;
  backend: ModelBackend;
  classes: string[];
  available: boolean;
  map50: number | null;
  map50_95: number | null;
  latency_ms: number | null;
  fps: number | null;
  benchmark_source: string | null;
  active: boolean;
}

// ---------- WebSocket ----------
export interface Track {
  id: number;
  cls: string;
  conf: number;
  /** normalised x1, y1, x2, y2 */
  bbox: [number, number, number, number];
}

export interface FrameMsg {
  camera_id: string;
  ts: string;
  frame: number;
  fps: number;
  latency_ms: number;
  width: number;
  height: number;
  tracks: Track[];
}

export type CameraStatusMsg = { camera_id: string } & CameraStatus;

export type WsMessage =
  | { type: "frame"; data: FrameMsg }
  | { type: "alert"; data: Incident }
  | { type: "incident_update"; data: Incident }
  | { type: "camera_status"; data: CameraStatusMsg }
  | { type: "pong" };

export type WsClientMessage =
  | { type: "subscribe"; camera_id: string }
  | { type: "unsubscribe"; camera_id: string }
  | { type: "ping" };
