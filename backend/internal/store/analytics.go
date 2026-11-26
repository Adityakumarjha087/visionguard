package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"visionguard/backend/internal/domain"
)

// ---------------------------------------------------------------- ingestion

// InsertCounts stores one per-second sample of object counts for a camera.
func (s *Store) InsertCounts(ctx context.Context, cameraID string, ts time.Time, counts map[string]int) error {
	batch := &pgx.Batch{}
	for cls, n := range counts {
		batch.Queue(`INSERT INTO detection_counts (ts, camera_id, object_class, count) VALUES ($1, $2, $3, $4)`,
			ts, cameraID, cls, n)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// TrackPoint is one sampled track position (normalised).
type TrackPoint struct {
	TrackID int32   `json:"track_id"`
	Class   string  `json:"cls"`
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
}

// InsertTrackPoints bulk-loads track points with COPY.
func (s *Store) InsertTrackPoints(ctx context.Context, cameraID string, ts time.Time, pts []TrackPoint) error {
	rows := make([][]any, 0, len(pts))
	for _, p := range pts {
		rows = append(rows, []any{ts, cameraID, p.TrackID, p.Class, p.X, p.Y})
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"track_points"},
		[]string{"ts", "camera_id", "track_id", "object_class", "x", "y"}, pgx.CopyFromRows(rows))
	return err
}

func (s *Store) InsertCrossing(ctx context.Context, cameraID, zoneID string, trackID int32, cls, dir string, ts time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO line_crossings (ts, camera_id, zone_id, track_id, object_class, direction)
		VALUES ($1, $2, $3, $4, $5, $6)`, ts, cameraID, zoneID, trackID, cls, dir)
	return mapErr(err)
}

// ---------------------------------------------------------------- queries

// Buckets maps the API bucket names to Postgres intervals.
var Buckets = map[string]string{"10s": "10 seconds", "1m": "1 minute", "5m": "5 minutes", "15m": "15 minutes", "1h": "1 hour"}

func (s *Store) Counts(ctx context.Context, cameraID string, from, to time.Time, bucket string) ([]domain.CountBucket, error) {
	interval, ok := Buckets[bucket]
	if !ok {
		return nil, fmt.Errorf("invalid bucket %q", bucket)
	}
	// Sum over cameras when no camera is selected (fleet view).
	q := `SELECT bucket, object_class, sum(avg_c)::float8, sum(max_c)::int FROM (
			SELECT time_bucket($1::interval, ts) AS bucket, camera_id, object_class,
			       avg(count) AS avg_c, max(count) AS max_c
			FROM detection_counts WHERE ts >= $2 AND ts < $3 AND ($4 = '' OR camera_id::text = $4)
			GROUP BY 1, 2, 3) per_cam
		  GROUP BY bucket, object_class ORDER BY bucket, object_class`
	rows, err := s.pool.Query(ctx, q, interval, from, to, cameraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CountBucket{}
	for rows.Next() {
		var b domain.CountBucket
		if err := rows.Scan(&b.Bucket, &b.ObjectClass, &b.AvgCount, &b.MaxCount); err != nil {
			return nil, err
		}
		b.AvgCount = math.Round(b.AvgCount*100) / 100
		out = append(out, b)
	}
	return out, rows.Err()
}

// Heatmap bins track points into a cols x rows grid (row-major) with SQL width_bucket.
func (s *Store) Heatmap(ctx context.Context, cameraID string, from, to time.Time, cols int) (domain.Heatmap, error) {
	rowsN := int(math.Round(float64(cols) * 9 / 16))
	hm := domain.Heatmap{Cols: cols, Rows: rowsN, Cells: make([]int, cols*rowsN), From: from, To: to}
	rows, err := s.pool.Query(ctx, `SELECT least(width_bucket(x, 0, 1, $4), $4) - 1 AS cx,
			least(width_bucket(y, 0, 1, $5), $5) - 1 AS cy, count(*)
		FROM track_points WHERE camera_id = $1 AND ts >= $2 AND ts < $3 GROUP BY 1, 2`,
		cameraID, from, to, cols, rowsN)
	if err != nil {
		return hm, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cx, cy, n int
		if err := rows.Scan(&cx, &cy, &n); err != nil {
			return hm, err
		}
		if cx < 0 || cy < 0 || cx >= cols || cy >= rowsN {
			continue
		}
		hm.Cells[cy*cols+cx] = n
		if n > hm.Max {
			hm.Max = n
		}
	}
	return hm, rows.Err()
}

func (s *Store) Crossings(ctx context.Context, cameraID string, from, to time.Time, bucket string) ([]domain.CrossingBucket, error) {
	interval, ok := Buckets[bucket]
	if !ok {
		return nil, fmt.Errorf("invalid bucket %q", bucket)
	}
	rows, err := s.pool.Query(ctx, `SELECT time_bucket($1::interval, lc.ts) AS bucket, lc.zone_id, coalesce(z.name, 'deleted line'),
			lc.direction, count(*)
		FROM line_crossings lc LEFT JOIN zones z ON z.id = lc.zone_id
		WHERE lc.ts >= $2 AND lc.ts < $3 AND ($4 = '' OR lc.camera_id::text = $4)
		GROUP BY 1, 2, 3, 4 ORDER BY 1, 2, 4`, interval, from, to, cameraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CrossingBucket{}
	for rows.Next() {
		var b domain.CrossingBucket
		if err := rows.Scan(&b.Bucket, &b.ZoneID, &b.ZoneName, &b.Direction, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SummaryCounts fills the DB-derived parts of the dashboard summary.
func (s *Store) SummaryCounts(ctx context.Context, cameraID string) (domain.Summary, error) {
	var sum domain.Summary
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM cameras WHERE $1 = '' OR id::text = $1),
		(SELECT count(*) FROM incidents WHERE status = 'open' AND ($1 = '' OR camera_id::text = $1)),
		(SELECT count(*) FROM incidents WHERE occurred_at > now() - interval '24 hours' AND ($1 = '' OR camera_id::text = $1)),
		(SELECT count(*) FROM line_crossings WHERE direction = 'in' AND ts > now() - interval '24 hours' AND ($1 = '' OR camera_id::text = $1)),
		(SELECT count(*) FROM line_crossings WHERE direction = 'out' AND ts > now() - interval '24 hours' AND ($1 = '' OR camera_id::text = $1))`,
		cameraID).Scan(&sum.CamerasTotal, &sum.OpenIncidents, &sum.Incidents24h, &sum.CrossingsIn24h, &sum.CrossingsOut24h)
	if err != nil {
		return sum, err
	}
	// "Now" = latest sample per camera/class within the last 15 seconds.
	err = s.pool.QueryRow(ctx, `SELECT
		coalesce(sum(count) FILTER (WHERE object_class = 'person'), 0),
		coalesce(sum(count) FILTER (WHERE object_class = ANY($2)), 0)
		FROM (SELECT DISTINCT ON (camera_id, object_class) camera_id, object_class, count
		      FROM detection_counts WHERE ts > now() - interval '15 seconds' AND ($1 = '' OR camera_id::text = $1)
		      ORDER BY camera_id, object_class, ts DESC) latest`, cameraID, domain.VehicleClasses,
	).Scan(&sum.PeopleNow, &sum.VehiclesNow)
	return sum, err
}

// ---------------------------------------------------------------- models

func (s *Store) UpsertModel(ctx context.Context, m domain.Model) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO models (id, name, backend, classes, available, map50, map50_95,
		latency_ms, fps, benchmark_source, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, backend = excluded.backend, classes = excluded.classes,
		available = excluded.available, map50 = coalesce(excluded.map50, models.map50),
		map50_95 = coalesce(excluded.map50_95, models.map50_95), latency_ms = coalesce(excluded.latency_ms, models.latency_ms),
		fps = coalesce(excluded.fps, models.fps), benchmark_source = coalesce(excluded.benchmark_source, models.benchmark_source),
		updated_at = now()`,
		m.ID, m.Name, m.Backend, m.Classes, m.Available, m.MAP50, m.MAP5095, m.LatencyMS, m.FPS, m.BenchmarkSource)
	return err
}

func (s *Store) ListModels(ctx context.Context) ([]domain.Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, backend, classes, available, map50, map50_95, latency_ms, fps,
		benchmark_source FROM models ORDER BY backend, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Model{}
	for rows.Next() {
		var m domain.Model
		var map50, map5095, lat, fps *float32
		if err := rows.Scan(&m.ID, &m.Name, &m.Backend, &m.Classes, &m.Available, &map50, &map5095, &lat, &fps,
			&m.BenchmarkSource); err != nil {
			return nil, err
		}
		m.MAP50, m.MAP5095, m.LatencyMS, m.FPS = f64(map50), f64(map5095), f64(lat), f64(fps)
		out = append(out, m)
	}
	return out, rows.Err()
}

func f64(v *float32) *float64 {
	if v == nil {
		return nil
	}
	f := math.Round(float64(*v)*100) / 100
	return &f
}
