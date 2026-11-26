-- VisionGuard schema: relational config + TimescaleDB hypertables for time-series events.
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    name          text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

CREATE TABLE cameras (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    source_type  text NOT NULL CHECK (source_type IN ('rtsp', 'webcam', 'file')),
    source_uri   text NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    privacy_mode boolean NOT NULL DEFAULT true,
    target_fps   real NOT NULL DEFAULT 10 CHECK (target_fps > 0 AND target_fps <= 30),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE zones (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    camera_id  uuid NOT NULL REFERENCES cameras (id) ON DELETE CASCADE,
    name       text NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('polygon', 'line')),
    points     jsonb NOT NULL,
    color      text NOT NULL DEFAULT '#22c55e',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX zones_camera_idx ON zones (camera_id);

CREATE TABLE rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    camera_id       uuid NOT NULL REFERENCES cameras (id) ON DELETE CASCADE,
    zone_id         uuid REFERENCES zones (id) ON DELETE CASCADE,
    name            text NOT NULL,
    type            text NOT NULL CHECK (type IN ('zone_intrusion', 'dwell_time', 'missing_ppe', 'crowd_count', 'line_crossing')),
    params          jsonb NOT NULL DEFAULT '{}',
    severity        text NOT NULL DEFAULT 'medium' CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    enabled         boolean NOT NULL DEFAULT true,
    notify_email    boolean NOT NULL DEFAULT false,
    notify_telegram boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rules_camera_idx ON rules (camera_id);

CREATE TABLE incidents (
    id                   uuid PRIMARY KEY,
    camera_id            uuid NOT NULL REFERENCES cameras (id) ON DELETE CASCADE,
    rule_id              uuid REFERENCES rules (id) ON DELETE SET NULL,
    rule_name            text NOT NULL,
    rule_type            text NOT NULL,
    severity             text NOT NULL,
    status               text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'resolved')),
    occurred_at          timestamptz NOT NULL,
    message              text NOT NULL,
    object_class         text NOT NULL DEFAULT '',
    track_ids            integer[] NOT NULL DEFAULT '{}',
    description          text,
    description_provider text,
    snapshot_path        text,
    clip_path            text,
    acknowledged_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    metadata             jsonb NOT NULL DEFAULT '{}',
    updated_at           timestamptz NOT NULL DEFAULT now(),
    search               tsvector GENERATED ALWAYS AS (
        to_tsvector('english', rule_name || ' ' || message || ' ' || coalesce(description, ''))
    ) STORED
);
CREATE INDEX incidents_occurred_idx ON incidents (occurred_at DESC);
CREATE INDEX incidents_camera_idx ON incidents (camera_id, occurred_at DESC);
CREATE INDEX incidents_search_idx ON incidents USING gin (search);

-- Time-series (no FKs on hypertables by design: high write rate, retention-managed).
CREATE TABLE detection_counts (
    ts           timestamptz NOT NULL,
    camera_id    uuid NOT NULL,
    object_class text NOT NULL,
    count        integer NOT NULL
);
SELECT create_hypertable('detection_counts', 'ts', chunk_time_interval => interval '1 day');
CREATE INDEX detection_counts_cam_idx ON detection_counts (camera_id, ts DESC);

CREATE TABLE track_points (
    ts           timestamptz NOT NULL,
    camera_id    uuid NOT NULL,
    track_id     integer NOT NULL,
    object_class text NOT NULL,
    x            real NOT NULL,
    y            real NOT NULL
);
SELECT create_hypertable('track_points', 'ts', chunk_time_interval => interval '1 day');
CREATE INDEX track_points_cam_idx ON track_points (camera_id, ts DESC);

CREATE TABLE line_crossings (
    ts           timestamptz NOT NULL,
    camera_id    uuid NOT NULL,
    zone_id      uuid NOT NULL,
    track_id     integer NOT NULL,
    object_class text NOT NULL,
    direction    text NOT NULL CHECK (direction IN ('in', 'out'))
);
SELECT create_hypertable('line_crossings', 'ts', chunk_time_interval => interval '1 day');
CREATE INDEX line_crossings_cam_idx ON line_crossings (camera_id, ts DESC);

SELECT add_retention_policy('track_points', interval '7 days');
SELECT add_retention_policy('detection_counts', interval '30 days');
SELECT add_retention_policy('line_crossings', interval '90 days');

-- statement-break
-- Per-minute rollup used for long time ranges on the analytics page.
CREATE MATERIALIZED VIEW detection_counts_1m
WITH (timescaledb.continuous) AS
SELECT time_bucket('1 minute', ts) AS bucket, camera_id, object_class,
       avg(count)::real AS avg_count, max(count) AS max_count
FROM detection_counts
GROUP BY bucket, camera_id, object_class
WITH NO DATA;
-- statement-break
SELECT add_continuous_aggregate_policy('detection_counts_1m',
    start_offset => interval '2 hours', end_offset => interval '1 minute', schedule_interval => interval '1 minute');
-- statement-break

CREATE TABLE models (
    id               text PRIMARY KEY,
    name             text NOT NULL,
    backend          text NOT NULL,
    classes          text[] NOT NULL DEFAULT '{}',
    available        boolean NOT NULL DEFAULT false,
    map50            real,
    map50_95         real,
    latency_ms       real,
    fps              real,
    benchmark_source text,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
