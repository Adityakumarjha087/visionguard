# VisionGuard API contract

Base URL: `http://localhost:20901` (host) / `http://api:8080` (compose network). All JSON uses
`snake_case`. All timestamps are RFC 3339 UTC strings. IDs are UUID strings unless noted.
Interactive docs: `GET /api/docs` (Swagger UI), spec at `GET /api/docs/openapi.yaml`.

## Errors

Every non-2xx response has the shape:

```json
{ "error": { "code": "validation_error", "message": "human readable", "details": {"field": "reason"} } }
```

Codes: `bad_request`, `validation_error`, `unauthorized`, `forbidden`, `not_found`, `conflict`,
`rate_limited`, `internal`.

## Auth & roles

Roles: `admin` > `operator` > `viewer`.

| Action | viewer | operator | admin |
|---|---|---|---|
| read cameras/zones/rules/incidents/analytics/models | yes | yes | yes |
| create/update/delete cameras, zones, rules; upload video; ack/resolve incidents | no | yes | yes |
| activate model, manage users | no | no | yes |

Access token: JWT (HS256, 15 min) sent as `Authorization: Bearer <token>`. For `<img>`/WebSocket
URLs where headers are impossible, `?access_token=<token>` is accepted on `GET` media/stream/ws
endpoints only. Refresh tokens are opaque, rotated on every refresh, 7 day TTL.

```
POST /api/v1/auth/register  {email, password(min 8), name}        -> 201 AuthResponse (role viewer)
POST /api/v1/auth/login     {email, password}                     -> 200 AuthResponse
POST /api/v1/auth/refresh   {refresh_token}                       -> 200 AuthResponse
POST /api/v1/auth/logout    {refresh_token}                       -> 204
GET  /api/v1/auth/me                                              -> 200 User
```

```ts
type Role = "admin" | "operator" | "viewer";
interface User { id: string; email: string; name: string; role: Role; created_at: string }
interface AuthResponse { access_token: string; refresh_token: string; token_type: "Bearer"; expires_in: number; user: User }
```

Users (admin): `GET /api/v1/users` -> `User[]`; `PATCH /api/v1/users/:id {role?, name?}` -> User;
`DELETE /api/v1/users/:id` -> 204.

Seeded demo accounts: `admin@visionguard.dev` / `admin12345`, `operator@visionguard.dev` /
`operator12345`, `viewer@visionguard.dev` / `viewer12345`.

## Cameras

```ts
type SourceType = "rtsp" | "webcam" | "file";
type CameraState = "starting" | "online" | "offline" | "error" | "disabled";
interface CameraStatus { state: CameraState; fps: number; latency_ms: number; message?: string; updated_at?: string }
interface Camera {
  id: string; name: string; source_type: SourceType;
  source_uri: string;         // rtsp://..., webcam index "0", or file path relative to video dir e.g. "samples/warehouse.mp4"
  enabled: boolean; privacy_mode: boolean; target_fps: number;
  created_at: string; updated_at: string;
  status: CameraStatus;       // live status reported by the inference service
}
```

```
GET    /api/v1/cameras                         -> Camera[]
POST   /api/v1/cameras   {name, source_type, source_uri, enabled?=true, privacy_mode?=true, target_fps?=10}  -> 201 Camera
GET    /api/v1/cameras/:id                     -> Camera
PATCH  /api/v1/cameras/:id {name?, source_uri?, enabled?, privacy_mode?, target_fps?} -> Camera
DELETE /api/v1/cameras/:id                     -> 204
POST   /api/v1/cameras/upload  multipart: file (video/*, <=200MB), name   -> 201 Camera (source_type "file")
GET    /api/v1/samples                         -> {name: string, source_uri: string, size_bytes: number}[]
GET    /api/v1/cameras/:id/stream              -> multipart/x-mixed-replace MJPEG (use as <img src>)
GET    /api/v1/cameras/:id/snapshot            -> image/jpeg (latest frame, used by the zone editor)
```

## Zones (coordinates normalised 0..1 relative to frame width/height)

```ts
type ZoneKind = "polygon" | "line";
interface Zone { id: string; camera_id: string; name: string; kind: ZoneKind; points: [number, number][]; color: string; created_at: string }
```

`polygon` needs >= 3 points; `line` needs exactly 2 points (crossing direction: "in" = moving
to the left side of the vector p0->p1).

```
GET    /api/v1/cameras/:id/zones                -> Zone[]
POST   /api/v1/cameras/:id/zones {name, kind, points, color?="#22c55e"} -> 201 Zone
PUT    /api/v1/zones/:id {name, kind, points, color}                     -> Zone
DELETE /api/v1/zones/:id                                                 -> 204
```

## Rules

```ts
type RuleType = "zone_intrusion" | "dwell_time" | "missing_ppe" | "crowd_count" | "line_crossing";
type Severity = "low" | "medium" | "high" | "critical";
interface RuleParams {
  classes?: string[];          // default per type: person (intrusion/ppe/crowd), vehicle classes (dwell)
  seconds?: number;            // dwell_time: stationary seconds threshold; missing_ppe: grace seconds
  max_count?: number;          // crowd_count: alert when count > max_count
  cooldown_seconds?: number;   // default 30
}
interface Rule {
  id: string; camera_id: string; zone_id: string | null; name: string; type: RuleType;
  params: RuleParams; severity: Severity; enabled: boolean;
  notify_email: boolean; notify_telegram: boolean; created_at: string;
}
```

`zone_intrusion`, `dwell_time` need a polygon zone; `line_crossing` needs a line zone;
`crowd_count` and `missing_ppe` accept an optional polygon zone (null = whole frame).

```
GET    /api/v1/rules?camera_id=                 -> Rule[]
POST   /api/v1/cameras/:id/rules {name, type, zone_id?, params?, severity?="medium", enabled?=true, notify_email?=false, notify_telegram?=false} -> 201 Rule
PUT    /api/v1/rules/:id   (same body)          -> Rule
DELETE /api/v1/rules/:id                        -> 204
```

## Incidents

```ts
type IncidentStatus = "open" | "acknowledged" | "resolved";
interface Incident {
  id: string; camera_id: string; camera_name: string; rule_id: string | null; rule_name: string;
  rule_type: RuleType; severity: Severity; status: IncidentStatus;
  occurred_at: string; message: string; object_class: string; track_ids: number[];
  description: string | null; description_provider: string | null;
  snapshot_url: string | null;   // relative, e.g. /api/v1/media/snapshots/<id>.jpg (append ?access_token=)
  clip_url: string | null;       // relative, e.g. /api/v1/media/clips/<id>.mp4
  acknowledged_by: string | null; updated_at: string;
}
interface Page<T> { items: T[]; total: number; page: number; page_size: number }
```

```
GET   /api/v1/incidents?q=&camera_id=&severity=&status=&rule_type=&from=&to=&page=1&page_size=20 -> Page<Incident>
GET   /api/v1/incidents/:id                      -> Incident
PATCH /api/v1/incidents/:id {status}             -> Incident
GET   /api/v1/media/snapshots/:file, /api/v1/media/clips/:file  -> binary
```

## Analytics

```
GET /api/v1/analytics/summary?camera_id=       -> {cameras_online, cameras_total, open_incidents, incidents_24h, people_now, vehicles_now, crossings_in_24h, crossings_out_24h}
GET /api/v1/analytics/counts?camera_id=&from=&to=&bucket=1m   -> {bucket: string, object_class: string, avg_count: number, max_count: number}[]
GET /api/v1/analytics/heatmap?camera_id=(required)&from=&to=&grid=32 -> {cols: number, rows: number, max: number, cells: number[]}  // row-major, rows = round(grid*9/16)
GET /api/v1/analytics/line-crossings?camera_id=&from=&to=&bucket=5m -> {bucket: string, zone_id: string, zone_name: string, direction: "in"|"out", count: number}[]
```

`bucket` accepts `10s|1m|5m|15m|1h`; `from` defaults to now-1h, `to` to now.

## Models

```ts
interface Model {
  id: string; name: string; backend: "color" | "motion" | "pytorch" | "onnx";
  classes: string[]; available: boolean;  // weights present on the inference host
  map50: number | null; map50_95: number | null; latency_ms: number | null; fps: number | null;
  benchmark_source: string | null; active: boolean;
}
```

```
GET  /api/v1/models                  -> Model[]
POST /api/v1/models/:id/activate     -> Model   (admin)
```

## WebSocket

`GET /api/v1/ws?access_token=<jwt>`. Client -> server:

```json
{"type": "subscribe", "camera_id": "<id>"}     // live frames for that camera (many allowed)
{"type": "unsubscribe", "camera_id": "<id>"}
```

Server -> client (`{type, data}` envelopes). Alerts, incident updates and camera status are
broadcast to every connected client; frames only to subscribers.

```ts
interface Track { id: number; cls: string; conf: number; bbox: [number, number, number, number] } // normalised x1,y1,x2,y2
interface FrameMsg { camera_id: string; ts: string; frame: number; fps: number; latency_ms: number; width: number; height: number; tracks: Track[] }
type WsMessage =
  | { type: "frame"; data: FrameMsg }
  | { type: "alert"; data: Incident }
  | { type: "incident_update"; data: Incident }
  | { type: "camera_status"; data: { camera_id: string } & CameraStatus }
  | { type: "pong" };
```

## Health & metrics

`GET /healthz` (liveness), `GET /readyz` (DB + Redis), `GET /metrics` (Prometheus).
