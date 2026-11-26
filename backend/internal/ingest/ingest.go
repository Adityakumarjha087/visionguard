// Package ingest turns inference-service events into persisted rows, WebSocket pushes and
// notifications, and publishes the desired camera configuration back to the inference service.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/store"
)

var eventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "vg_events_ingested_total", Help: "Inference events processed by type and outcome"}, []string{"type", "status"})

func init() { prometheus.MustRegister(eventsTotal) }

// Broadcaster pushes messages to WebSocket clients.
type Broadcaster interface {
	Broadcast(msgType string, data any)
}

// Notifier delivers external notifications for new incidents.
type Notifier interface {
	Notify(inc domain.Incident)
}

// Processor handles vg:events entries.
type Processor struct {
	Store    *store.Store
	Hub      Broadcaster
	Notifier Notifier
	Syncer   *Syncer
}

type alertEvent struct {
	ID           string    `json:"id"`
	CameraID     string    `json:"camera_id"`
	RuleID       string    `json:"rule_id"`
	RuleName     string    `json:"rule_name"`
	RuleType     string    `json:"rule_type"`
	Severity     string    `json:"severity"`
	OccurredAt   time.Time `json:"occurred_at"`
	TrackIDs     []int32   `json:"track_ids"`
	ObjectClass  string    `json:"object_class"`
	Message      string    `json:"message"`
	ZoneID       *string   `json:"zone_id"`
	SnapshotPath *string   `json:"snapshot_path"`
	Privacy      bool      `json:"privacy_applied"`
}

type updateEvent struct {
	ID                  string  `json:"id"`
	Description         *string `json:"description"`
	DescriptionProvider *string `json:"description_provider"`
	ClipPath            *string `json:"clip_path"`
}

type statsEvent struct {
	CameraID string         `json:"camera_id"`
	TS       time.Time      `json:"ts"`
	Counts   map[string]int `json:"counts"`
}

type pointsEvent struct {
	CameraID string             `json:"camera_id"`
	TS       time.Time          `json:"ts"`
	Points   []store.TrackPoint `json:"points"`
}

type crossEvent struct {
	CameraID  string    `json:"camera_id"`
	ZoneID    string    `json:"zone_id"`
	TrackID   int32     `json:"track_id"`
	Class     string    `json:"cls"`
	Direction string    `json:"direction"`
	TS        time.Time `json:"ts"`
}

// Handle implements bus.Handler. Unknown cameras/zones (deleted) are dropped, not retried.
func (p *Processor) Handle(ctx context.Context, ev bus.Event) error {
	err := p.handle(ctx, ev)
	status := "ok"
	if errors.Is(err, domain.ErrNotFound) {
		status, err = "dropped", nil
	} else if err != nil {
		status = "error"
	}
	eventsTotal.WithLabelValues(ev.Type, status).Inc()
	return err
}

func (p *Processor) handle(ctx context.Context, ev bus.Event) error {
	switch ev.Type {
	case "alert":
		var a alertEvent
		if err := json.Unmarshal(ev.Data, &a); err != nil {
			return fmt.Errorf("%w: bad alert payload", domain.ErrNotFound)
		}
		var ruleID *string
		if a.RuleID != "" {
			ruleID = &a.RuleID
		}
		inc, created, err := p.Store.InsertIncident(ctx, store.NewIncident{
			ID: a.ID, CameraID: a.CameraID, RuleID: ruleID, RuleName: a.RuleName, RuleType: a.RuleType,
			Severity: a.Severity, OccurredAt: a.OccurredAt, Message: a.Message, ObjectClass: a.ObjectClass,
			TrackIDs: a.TrackIDs, SnapshotPath: a.SnapshotPath,
			Metadata: map[string]any{"zone_id": a.ZoneID, "privacy_applied": a.Privacy},
		})
		if err != nil {
			return err
		}
		if created {
			p.Hub.Broadcast("alert", inc)
			if p.Notifier != nil {
				p.Notifier.Notify(inc)
			}
		}
	case "incident_update":
		var u updateEvent
		if err := json.Unmarshal(ev.Data, &u); err != nil {
			return fmt.Errorf("%w: bad update payload", domain.ErrNotFound)
		}
		inc, err := p.Store.EnrichIncident(ctx, u.ID, store.IncidentUpdate{
			Description: u.Description, DescriptionProvider: u.DescriptionProvider, ClipPath: u.ClipPath,
		})
		if err != nil {
			return err
		}
		p.Hub.Broadcast("incident_update", inc)
	case "stats":
		var s statsEvent
		if err := json.Unmarshal(ev.Data, &s); err != nil {
			return fmt.Errorf("%w: bad stats payload", domain.ErrNotFound)
		}
		counts := map[string]int{"person": 0, "car": 0}
		for k, v := range s.Counts {
			if k != "helmet" {
				counts[k] = v
			}
		}
		return p.Store.InsertCounts(ctx, s.CameraID, s.TS, counts)
	case "track_points":
		var s pointsEvent
		if err := json.Unmarshal(ev.Data, &s); err != nil {
			return fmt.Errorf("%w: bad points payload", domain.ErrNotFound)
		}
		return p.Store.InsertTrackPoints(ctx, s.CameraID, s.TS, s.Points)
	case "line_cross":
		var c crossEvent
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			return fmt.Errorf("%w: bad crossing payload", domain.ErrNotFound)
		}
		return p.Store.InsertCrossing(ctx, c.CameraID, c.ZoneID, c.TrackID, c.Class, c.Direction, c.TS)
	case "camera_status":
		var raw map[string]any
		if err := json.Unmarshal(ev.Data, &raw); err == nil {
			p.Hub.Broadcast("camera_status", raw)
		}
	case "model_catalog":
		var doc struct {
			Models []domain.Model `json:"models"`
		}
		if err := json.Unmarshal(ev.Data, &doc); err != nil {
			return fmt.Errorf("%w: bad catalog payload", domain.ErrNotFound)
		}
		for _, m := range doc.Models {
			if err := p.Store.UpsertModel(ctx, m); err != nil {
				return err
			}
		}
	default:
		slog.Debug("ignoring event", "type", ev.Type)
	}
	return nil
}

// ---------------------------------------------------------------- desired state

// ActiveModelKey is the settings key holding the globally active detector.
const ActiveModelKey = "active_model"

// Syncer rebuilds and publishes the desired-state document.
type Syncer struct {
	Store        *store.Store
	Bus          *bus.Bus
	DefaultModel string
	mu           sync.Mutex
}

// ActiveModel returns the configured active model id.
func (s *Syncer) ActiveModel(ctx context.Context) string {
	var model string
	if err := s.Store.GetSetting(ctx, ActiveModelKey, &model); err != nil || model == "" {
		return s.DefaultModel
	}
	return model
}

// Sync publishes the current configuration; safe for concurrent callers.
func (s *Syncer) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cams, err := s.Store.ListCameras(ctx)
	if err != nil {
		return err
	}
	zones, err := s.Store.ListZones(ctx, "")
	if err != nil {
		return err
	}
	rules, err := s.Store.ListRules(ctx, "")
	if err != nil {
		return err
	}
	byCam := map[string]*bus.CameraConfig{}
	doc := bus.ConfigDoc{Version: time.Now().UnixMilli(), ActiveModel: s.ActiveModel(ctx), Cameras: []bus.CameraConfig{}}
	for _, c := range cams {
		doc.Cameras = append(doc.Cameras, bus.CameraConfig{Camera: c, Zones: []domain.Zone{}, Rules: []domain.Rule{}})
	}
	for i := range doc.Cameras {
		byCam[doc.Cameras[i].ID] = &doc.Cameras[i]
	}
	for _, z := range zones {
		if c, ok := byCam[z.CameraID]; ok {
			c.Zones = append(c.Zones, z)
		}
	}
	for _, r := range rules {
		if c, ok := byCam[r.CameraID]; ok {
			c.Rules = append(c.Rules, r)
		}
	}
	return s.Bus.PublishConfig(ctx, doc)
}

// Run re-publishes periodically so a flushed Redis self-heals.
func (s *Syncer) Run(ctx context.Context, every time.Duration) {
	if err := s.Sync(ctx); err != nil {
		slog.Warn("initial config sync failed", "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Sync(ctx); err != nil {
				slog.Warn("config sync failed", "err", err)
			}
		}
	}
}
