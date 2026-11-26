// Package seed loads realistic demo data: users, sample cameras with zones/rules, model
// benchmarks and a few hours of historical analytics so the dashboard is useful immediately.
package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"time"

	"github.com/google/uuid"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/store"
)

// DemoUsers are the seeded accounts (documented in the README).
var DemoUsers = []struct {
	Email, Name, Password string
	Role                  domain.Role
}{
	{"admin@visionguard.dev", "Ada Admin", "admin12345", domain.RoleAdmin},
	{"operator@visionguard.dev", "Oscar Operator", "operator12345", domain.RoleOperator},
	{"viewer@visionguard.dev", "Vera Viewer", "viewer12345", domain.RoleViewer},
}

// WarehouseCamera is the primary demo camera name (used by e2e tests).
const WarehouseCamera = "Warehouse Floor (sample)"

func ptr[T any](v T) *T { return &v }

// Run seeds idempotently: users are upserted by email; cameras/history only when none exist.
func Run(ctx context.Context, st *store.Store, benchmarksFile string, history time.Duration) error {
	for _, u := range DemoUsers {
		if _, err := st.UserByEmail(ctx, u.Email); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		hash, err := auth.HashPassword(u.Password)
		if err != nil {
			return err
		}
		if _, err := st.CreateUser(ctx, u.Email, u.Name, hash, u.Role); err != nil {
			return fmt.Errorf("seed user %s: %w", u.Email, err)
		}
	}
	if err := seedModels(ctx, st, benchmarksFile); err != nil {
		slog.Warn("model seed skipped", "err", err)
	}
	cams, err := st.ListCameras(ctx)
	if err != nil {
		return err
	}
	if len(cams) > 0 {
		slog.Info("cameras already present; skipping camera/history seed")
		return nil
	}
	wh, err := seedWarehouse(ctx, st)
	if err != nil {
		return err
	}
	street, err := seedStreet(ctx, st)
	if err != nil {
		return err
	}
	if history > 0 {
		if err := seedHistory(ctx, st, wh, street, history); err != nil {
			return err
		}
	}
	slog.Info("seed complete", "warehouse", wh.camera.ID, "street", street.camera.ID)
	return nil
}

type seeded struct {
	camera domain.Camera
	zones  map[string]domain.Zone
	rules  map[string]domain.Rule
}

func seedWarehouse(ctx context.Context, st *store.Store) (seeded, error) {
	s := seeded{zones: map[string]domain.Zone{}, rules: map[string]domain.Rule{}}
	cam, err := st.CreateCamera(ctx, store.CameraInput{Name: WarehouseCamera, SourceType: "file",
		SourceURI: "samples/warehouse.mp4", Enabled: true, PrivacyMode: true, TargetFPS: 10})
	if err != nil {
		return s, err
	}
	s.camera = cam
	zones := []store.ZoneInput{
		{Name: "Restricted area", Kind: "polygon", Color: "#ef4444", Points: []domain.Point{{0.70, 0.10}, {0.95, 0.10}, {0.95, 0.45}, {0.70, 0.45}}},
		{Name: "Loading bay", Kind: "polygon", Color: "#f59e0b", Points: []domain.Point{{0.04, 0.58}, {0.40, 0.58}, {0.40, 0.97}, {0.04, 0.97}}},
		{Name: "Assembly point", Kind: "polygon", Color: "#3b82f6", Points: []domain.Point{{0.40, 0.40}, {0.66, 0.40}, {0.66, 0.75}, {0.40, 0.75}}},
		{Name: "Aisle counter", Kind: "line", Color: "#22c55e", Points: []domain.Point{{0.5, 0.02}, {0.5, 0.98}}},
	}
	for _, z := range zones {
		created, err := st.CreateZone(ctx, cam.ID, z)
		if err != nil {
			return s, err
		}
		s.zones[z.Name] = created
	}
	rules := []store.RuleInput{
		{Name: "Person in restricted area", Type: "zone_intrusion", ZoneID: ptr(s.zones["Restricted area"].ID), Severity: "high",
			Params: domain.RuleParams{Classes: []string{"person"}, CooldownSeconds: ptr(20.0)}, Enabled: true, NotifyEmail: true},
		{Name: "Vehicle parked in loading bay", Type: "dwell_time", ZoneID: ptr(s.zones["Loading bay"].ID), Severity: "medium",
			Params: domain.RuleParams{Classes: []string{"car", "truck", "bus"}, Seconds: ptr(20.0), CooldownSeconds: ptr(30.0)}, Enabled: true},
		{Name: "Worker without helmet", Type: "missing_ppe", Severity: "high",
			Params: domain.RuleParams{Seconds: ptr(3.0), CooldownSeconds: ptr(20.0)}, Enabled: true, NotifyTelegram: true},
		{Name: "Crowd at assembly point", Type: "crowd_count", ZoneID: ptr(s.zones["Assembly point"].ID), Severity: "low",
			Params: domain.RuleParams{MaxCount: ptr(4), CooldownSeconds: ptr(30.0)}, Enabled: true},
	}
	for _, r := range rules {
		created, err := st.CreateRule(ctx, cam.ID, r)
		if err != nil {
			return s, err
		}
		s.rules[r.Name] = created
	}
	return s, nil
}

func seedStreet(ctx context.Context, st *store.Store) (seeded, error) {
	s := seeded{zones: map[string]domain.Zone{}, rules: map[string]domain.Rule{}}
	cam, err := st.CreateCamera(ctx, store.CameraInput{Name: "Street Crossing (sample)", SourceType: "file",
		SourceURI: "samples/street.mp4", Enabled: true, PrivacyMode: true, TargetFPS: 8})
	if err != nil {
		return s, err
	}
	s.camera = cam
	line, err := st.CreateZone(ctx, cam.ID, store.ZoneInput{Name: "Crosswalk", Kind: "line", Color: "#22c55e",
		Points: []domain.Point{{0.45, 0.02}, {0.45, 0.98}}})
	if err != nil {
		return s, err
	}
	s.zones[line.Name] = line
	r, err := st.CreateRule(ctx, cam.ID, store.RuleInput{Name: "Vehicle crossing count", Type: "line_crossing",
		ZoneID: &line.ID, Severity: "low", Params: domain.RuleParams{Classes: []string{"car"}, CooldownSeconds: ptr(60.0)},
		Enabled: true})
	if err != nil {
		return s, err
	}
	s.rules[r.Name] = r
	return s, nil
}

// seedHistory writes plausible time-series for the last `span` (business-hours shaped).
func seedHistory(ctx context.Context, st *store.Store, wh, street seeded, span time.Duration) error {
	rng := rand.New(rand.NewPCG(42, 7))
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-span)
	for _, cam := range []seeded{wh, street} {
		var points []store.TrackPoint
		for ts := start; ts.Before(now.Add(-time.Minute)); ts = ts.Add(15 * time.Second) {
			hour := float64(ts.Hour()) + float64(ts.Minute())/60
			activity := 0.5 + 0.5*math.Sin((hour-6)/24*2*math.Pi)
			people := int(math.Round(activity*6 + rng.Float64()*2))
			cars := int(math.Round(activity*2 + rng.Float64()))
			if err := st.InsertCounts(ctx, cam.camera.ID, ts, map[string]int{"person": people, "car": cars}); err != nil {
				return err
			}
			for i := 0; i < people; i++ {
				// Two walkways + loading bay hotspot.
				x := clamp(0.15+rng.Float64()*0.7, 0, 1)
				y := 0.7
				if i%2 == 0 {
					y = 0.48
				}
				points = append(points, store.TrackPoint{TrackID: int32(i), Class: "person",
					X: float32(x), Y: float32(clamp(y+rng.NormFloat64()*0.05, 0, 1))})
			}
			for i := 0; i < cars; i++ {
				points = append(points, store.TrackPoint{TrackID: int32(100 + i), Class: "car",
					X: float32(clamp(0.2+rng.NormFloat64()*0.05, 0, 1)), Y: float32(clamp(0.86+rng.NormFloat64()*0.02, 0, 1))})
			}
			if rng.Float64() < 0.25*activity {
				dir := "in"
				if rng.IntN(2) == 0 {
					dir = "out"
				}
				var line domain.Zone
				for _, z := range cam.zones {
					if z.Kind == "line" {
						line = z
					}
				}
				if line.ID != "" {
					if err := st.InsertCrossing(ctx, cam.camera.ID, line.ID, int32(rng.IntN(500)), "person", dir, ts); err != nil {
						return err
					}
				}
			}
		}
		if err := st.InsertTrackPoints(ctx, cam.camera.ID, now, points); err != nil {
			return err
		}
	}
	type demo struct {
		rule, msg, cls, desc string
		ago             time.Duration
		status          string
	}
	incidents := []demo{
		{"Person in restricted area", "person #12 entered restricted zone 'Restricted area'", "person",
			"A person walked into the fenced restricted area in the upper-right of the frame near the racking. The zone requires authorised access.", 5 * time.Hour, "resolved"},
		{"Worker without helmet", "worker #31 without helmet for 4s", "person",
			"A worker crossing the central aisle is not wearing a safety helmet while other staff nearby are. PPE is mandatory on the warehouse floor.", 4 * time.Hour, "acknowledged"},
		{"Vehicle parked in loading bay", "car #4 stationary for 24s in 'Loading bay' (limit 20s)", "car",
			"A blue vehicle has been stationary in the loading bay in the lower-left of the frame beyond the allowed dwell time, blocking the bay.", 3 * time.Hour, "open"},
		{"Crowd at assembly point", "Crowd of 6 people in Assembly point (limit 4)", "person",
			"Six people are gathered at the assembly point in the centre of the frame, above the configured occupancy of four.", 2 * time.Hour, "open"},
		{"Person in restricted area", "person #57 entered restricted zone 'Restricted area'", "person",
			"An individual entered the restricted area and lingered beside the shelving for several seconds before leaving.", 40 * time.Minute, "open"},
	}
	for _, d := range incidents {
		rule := wh.rules[d.rule]
		id := uuid.NewString()
		inc, _, err := st.InsertIncident(ctx, store.NewIncident{ID: id, CameraID: wh.camera.ID, RuleID: &rule.ID,
			RuleName: rule.Name, RuleType: rule.Type, Severity: rule.Severity, OccurredAt: now.Add(-d.ago),
			Message: d.msg, ObjectClass: d.cls, TrackIDs: []int32{int32(rng.IntN(90) + 1)},
			Metadata: map[string]any{"seeded": true}})
		if err != nil {
			return err
		}
		provider := "fake"
		if _, err := st.EnrichIncident(ctx, inc.ID, store.IncidentUpdate{Description: &d.desc, DescriptionProvider: &provider}); err != nil {
			return err
		}
		if d.status != "open" {
			admin, err := st.UserByEmail(ctx, DemoUsers[1].Email)
			if err != nil {
				return err
			}
			if _, err := st.SetIncidentStatus(ctx, inc.ID, d.status, admin.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedModels(ctx context.Context, st *store.Store, path string) error {
	base := []domain.Model{
		{ID: "color-synthetic", Name: "Colour segmentation (synthetic demo)", Backend: "color", Classes: []string{"person", "car", "helmet"}, Available: true},
		{ID: "motion-mog2", Name: "MOG2 motion detector", Backend: "motion", Classes: []string{"object"}, Available: true},
		{ID: "yolo11n-onnx", Name: "YOLO11n COCO (ONNX Runtime)", Backend: "onnx", Classes: []string{"bicycle", "bus", "car", "motorcycle", "person", "truck"}},
		{ID: "yolo11n", Name: "YOLO11n COCO (PyTorch)", Backend: "pytorch", Classes: []string{"bicycle", "bus", "car", "motorcycle", "person", "truck"}},
		{ID: "ppe-yolo11n-onnx", Name: "YOLO11n fine-tuned PPE (ONNX)", Backend: "onnx", Classes: []string{"person", "helmet", "no_helmet"}},
	}
	bench := map[string]map[string]any{}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var doc struct {
				Models map[string]map[string]any `json:"models"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				bench = doc.Models
			}
		}
	}
	for _, m := range base {
		if b, ok := bench[m.ID]; ok {
			m.MAP50, m.MAP5095, m.LatencyMS, m.FPS = num(b["map50"]), num(b["map50_95"]), num(b["latency_ms"]), num(b["fps"])
			if src, ok := b["source"].(string); ok {
				m.BenchmarkSource = &src
			}
		}
		if err := st.UpsertModel(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func num(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
