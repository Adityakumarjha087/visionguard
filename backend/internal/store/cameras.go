package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"visionguard/backend/internal/domain"
)

// ---------------------------------------------------------------- cameras

const cameraCols = `id, name, source_type, source_uri, enabled, privacy_mode, target_fps, created_at, updated_at`

func scanCamera(row pgx.Row) (domain.Camera, error) {
	var c domain.Camera
	var fps float32
	err := row.Scan(&c.ID, &c.Name, &c.SourceType, &c.SourceURI, &c.Enabled, &c.PrivacyMode, &fps, &c.CreatedAt, &c.UpdatedAt)
	c.TargetFPS = float64(fps)
	return c, mapErr(err)
}

// CameraInput is the writable subset of a camera.
type CameraInput struct {
	Name        string
	SourceType  string
	SourceURI   string
	Enabled     bool
	PrivacyMode bool
	TargetFPS   float64
}

func (s *Store) CreateCamera(ctx context.Context, in CameraInput) (domain.Camera, error) {
	return scanCamera(s.pool.QueryRow(ctx, `INSERT INTO cameras (name, source_type, source_uri, enabled, privacy_mode, target_fps)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+cameraCols,
		in.Name, in.SourceType, in.SourceURI, in.Enabled, in.PrivacyMode, in.TargetFPS))
}

func (s *Store) ListCameras(ctx context.Context) ([]domain.Camera, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cameraCols+` FROM cameras ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Camera{}
	for rows.Next() {
		c, err := scanCamera(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCamera(ctx context.Context, id string) (domain.Camera, error) {
	return scanCamera(s.pool.QueryRow(ctx, `SELECT `+cameraCols+` FROM cameras WHERE id = $1`, id))
}

// CameraPatch carries optional updates (nil = unchanged).
type CameraPatch struct {
	Name        *string
	SourceURI   *string
	Enabled     *bool
	PrivacyMode *bool
	TargetFPS   *float64
}

func (s *Store) UpdateCamera(ctx context.Context, id string, p CameraPatch) (domain.Camera, error) {
	return scanCamera(s.pool.QueryRow(ctx, `UPDATE cameras SET
		name = coalesce($2, name), source_uri = coalesce($3, source_uri), enabled = coalesce($4, enabled),
		privacy_mode = coalesce($5, privacy_mode), target_fps = coalesce($6, target_fps), updated_at = now()
		WHERE id = $1 RETURNING `+cameraCols, id, p.Name, p.SourceURI, p.Enabled, p.PrivacyMode, p.TargetFPS))
}

// DeleteCamera removes a camera, its config (cascade) and its time-series rows.
func (s *Store) DeleteCamera(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `DELETE FROM cameras WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	for _, table := range []string{"detection_counts", "track_points", "line_crossings"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE camera_id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------- zones

const zoneCols = `id, camera_id, name, kind, points, color, created_at`

func scanZone(row pgx.Row) (domain.Zone, error) {
	var z domain.Zone
	var raw []byte
	if err := row.Scan(&z.ID, &z.CameraID, &z.Name, &z.Kind, &raw, &z.Color, &z.CreatedAt); err != nil {
		return z, mapErr(err)
	}
	return z, json.Unmarshal(raw, &z.Points)
}

// ZoneInput is the writable subset of a zone.
type ZoneInput struct {
	Name   string
	Kind   string
	Points []domain.Point
	Color  string
}

func (s *Store) CreateZone(ctx context.Context, cameraID string, in ZoneInput) (domain.Zone, error) {
	pts, _ := json.Marshal(in.Points)
	return scanZone(s.pool.QueryRow(ctx, `INSERT INTO zones (camera_id, name, kind, points, color)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+zoneCols, cameraID, in.Name, in.Kind, pts, in.Color))
}

func (s *Store) UpdateZone(ctx context.Context, id string, in ZoneInput) (domain.Zone, error) {
	pts, _ := json.Marshal(in.Points)
	return scanZone(s.pool.QueryRow(ctx, `UPDATE zones SET name = $2, kind = $3, points = $4, color = $5
		WHERE id = $1 RETURNING `+zoneCols, id, in.Name, in.Kind, pts, in.Color))
}

func (s *Store) GetZone(ctx context.Context, id string) (domain.Zone, error) {
	return scanZone(s.pool.QueryRow(ctx, `SELECT `+zoneCols+` FROM zones WHERE id = $1`, id))
}

func (s *Store) ListZones(ctx context.Context, cameraID string) ([]domain.Zone, error) {
	q := `SELECT ` + zoneCols + ` FROM zones`
	args := []any{}
	if cameraID != "" {
		q += ` WHERE camera_id = $1`
		args = append(args, cameraID)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Zone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (s *Store) DeleteZone(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM zones WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- rules

const ruleCols = `id, camera_id, zone_id, name, type, params, severity, enabled, notify_email, notify_telegram, created_at`

func scanRule(row pgx.Row) (domain.Rule, error) {
	var r domain.Rule
	var raw []byte
	if err := row.Scan(&r.ID, &r.CameraID, &r.ZoneID, &r.Name, &r.Type, &raw, &r.Severity, &r.Enabled,
		&r.NotifyEmail, &r.NotifyTelegram, &r.CreatedAt); err != nil {
		return r, mapErr(err)
	}
	return r, json.Unmarshal(raw, &r.Params)
}

// RuleInput is the writable subset of a rule.
type RuleInput struct {
	ZoneID         *string
	Name           string
	Type           string
	Params         domain.RuleParams
	Severity       string
	Enabled        bool
	NotifyEmail    bool
	NotifyTelegram bool
}

func (s *Store) CreateRule(ctx context.Context, cameraID string, in RuleInput) (domain.Rule, error) {
	params, _ := json.Marshal(in.Params)
	return scanRule(s.pool.QueryRow(ctx, `INSERT INTO rules (camera_id, zone_id, name, type, params, severity, enabled,
		notify_email, notify_telegram) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+ruleCols,
		cameraID, in.ZoneID, in.Name, in.Type, params, in.Severity, in.Enabled, in.NotifyEmail, in.NotifyTelegram))
}

func (s *Store) UpdateRule(ctx context.Context, id string, in RuleInput) (domain.Rule, error) {
	params, _ := json.Marshal(in.Params)
	return scanRule(s.pool.QueryRow(ctx, `UPDATE rules SET zone_id = $2, name = $3, type = $4, params = $5,
		severity = $6, enabled = $7, notify_email = $8, notify_telegram = $9 WHERE id = $1 RETURNING `+ruleCols,
		id, in.ZoneID, in.Name, in.Type, params, in.Severity, in.Enabled, in.NotifyEmail, in.NotifyTelegram))
}

func (s *Store) GetRule(ctx context.Context, id string) (domain.Rule, error) {
	return scanRule(s.pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM rules WHERE id = $1`, id))
}

func (s *Store) ListRules(ctx context.Context, cameraID string) ([]domain.Rule, error) {
	q := `SELECT ` + ruleCols + ` FROM rules`
	args := []any{}
	if cameraID != "" {
		q += ` WHERE camera_id = $1`
		args = append(args, cameraID)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM rules WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
