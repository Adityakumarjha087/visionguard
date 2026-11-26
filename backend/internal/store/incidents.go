package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"visionguard/backend/internal/domain"
)

const incidentSelect = `SELECT i.id, i.camera_id, c.name, i.rule_id, i.rule_name, i.rule_type, i.severity, i.status,
	i.occurred_at, i.message, i.object_class, i.track_ids, i.description, i.description_provider,
	i.snapshot_path, i.clip_path, i.acknowledged_by, i.updated_at,
	coalesce(r.notify_email, false), coalesce(r.notify_telegram, false)
	FROM incidents i JOIN cameras c ON c.id = i.camera_id LEFT JOIN rules r ON r.id = i.rule_id`

func scanIncident(row pgx.Row) (domain.Incident, error) {
	var in domain.Incident
	err := row.Scan(&in.ID, &in.CameraID, &in.CameraName, &in.RuleID, &in.RuleName, &in.RuleType, &in.Severity,
		&in.Status, &in.OccurredAt, &in.Message, &in.ObjectClass, &in.TrackIDs, &in.Description,
		&in.DescriptionProvider, &in.SnapshotPath, &in.ClipPath, &in.AcknowledgedBy, &in.UpdatedAt,
		&in.NotifyEmail, &in.NotifyTelegram)
	if err != nil {
		return in, mapErr(err)
	}
	in.SnapshotURL = mediaURL(in.SnapshotPath)
	in.ClipURL = mediaURL(in.ClipPath)
	if in.TrackIDs == nil {
		in.TrackIDs = []int32{}
	}
	return in, nil
}

func mediaURL(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	u := "/api/v1/media/" + strings.TrimLeft(*p, "/")
	return &u
}

// NewIncident is what the inference service reports in an `alert` event.
type NewIncident struct {
	ID           string
	CameraID     string
	RuleID       *string
	RuleName     string
	RuleType     string
	Severity     string
	OccurredAt   time.Time
	Message      string
	ObjectClass  string
	TrackIDs     []int32
	SnapshotPath *string
	Metadata     map[string]any
}

// InsertIncident is idempotent on id (at-least-once stream delivery). created reports a new row.
func (s *Store) InsertIncident(ctx context.Context, n NewIncident) (inc domain.Incident, created bool, err error) {
	if n.Metadata == nil {
		n.Metadata = map[string]any{}
	}
	if n.TrackIDs == nil {
		n.TrackIDs = []int32{}
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO incidents (id, camera_id, rule_id, rule_name, rule_type, severity,
		occurred_at, message, object_class, track_ids, snapshot_path, metadata)
		VALUES ($1, $2, (SELECT id FROM rules WHERE id::text = $3), $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO NOTHING`,
		n.ID, n.CameraID, n.RuleID, n.RuleName, n.RuleType, n.Severity, n.OccurredAt, n.Message, n.ObjectClass,
		n.TrackIDs, n.SnapshotPath, n.Metadata)
	if err != nil {
		return inc, false, mapErr(err)
	}
	inc, err = s.GetIncident(ctx, n.ID)
	return inc, tag.RowsAffected() == 1, err
}

// IncidentUpdate is an async enrichment from the inference service (description, clip).
type IncidentUpdate struct {
	Description         *string
	DescriptionProvider *string
	ClipPath            *string
}

func (s *Store) EnrichIncident(ctx context.Context, id string, u IncidentUpdate) (domain.Incident, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE incidents SET description = coalesce($2, description),
		description_provider = coalesce($3, description_provider), clip_path = coalesce($4, clip_path),
		updated_at = now() WHERE id = $1`, id, u.Description, u.DescriptionProvider, u.ClipPath)
	if err != nil {
		return domain.Incident{}, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Incident{}, domain.ErrNotFound
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) GetIncident(ctx context.Context, id string) (domain.Incident, error) {
	return scanIncident(s.pool.QueryRow(ctx, incidentSelect+` WHERE i.id = $1`, id))
}

func (s *Store) SetIncidentStatus(ctx context.Context, id, status, userID string) (domain.Incident, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE incidents SET status = $2,
		acknowledged_by = CASE WHEN $2 = 'open' THEN NULL ELSE coalesce(acknowledged_by, $3::uuid) END,
		updated_at = now() WHERE id = $1`, id, status, userID)
	if err != nil {
		return domain.Incident{}, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Incident{}, domain.ErrNotFound
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) ListIncidents(ctx context.Context, f domain.IncidentFilter) (domain.Page[domain.Incident], error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, q, "%"+q+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(i.search @@ websearch_to_tsquery('english', $%d) OR i.message ILIKE $%d OR c.name ILIKE $%d)", n-1, n, n))
	}
	if f.CameraID != "" {
		add("i.camera_id::text = $%d", f.CameraID)
	}
	if f.Severity != "" {
		add("i.severity = $%d", f.Severity)
	}
	if f.Status != "" {
		add("i.status = $%d", f.Status)
	}
	if f.RuleType != "" {
		add("i.rule_type = $%d", f.RuleType)
	}
	if f.From != nil {
		add("i.occurred_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("i.occurred_at <= $%d", *f.To)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	page := domain.Page[domain.Incident]{Items: []domain.Incident{}, Page: f.Page, PageSize: f.PageSize}
	countSQL := `SELECT count(*) FROM incidents i JOIN cameras c ON c.id = i.camera_id` + clause
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := s.pool.Query(ctx, incidentSelect+clause+
		fmt.Sprintf(" ORDER BY i.occurred_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, inc)
	}
	return page, rows.Err()
}
