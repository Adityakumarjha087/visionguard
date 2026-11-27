// Package sim is an in-process stand-in for the inference service, used by the hosted demo
// where the CPU vision pipeline does not fit the instance. It reads the same desired-state
// document from Redis (vg:config) and speaks the same contract back (vg:frames, vg:events,
// vg:status:<cam>), so the API ingests simulated detections, tracks, line crossings and
// rule-driven incidents exactly as it would real ones. Frames are rendered synthetically.
package sim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/store"
)

// Provider is the description_provider recorded on simulated incidents.
const Provider = "simulator"

// Options tunes the simulator; zero values pick sensible defaults.
type Options struct {
	Tick          time.Duration // simulation step (also the reported frame interval)
	MinCooldown   time.Duration // lower bound on per-rule alert cooldown, keeps the DB small
	StatsInterval time.Duration
	DescDelay     time.Duration // delay before the "vision LLM" description is published
	DataDir       string        // snapshots are written to <DataDir>/media/snapshots
	Seed          uint64
}

// Simulator owns the simulated cameras.
type Simulator struct {
	bus  *bus.Bus
	opts Options
	rng  *rand.Rand

	mu      sync.Mutex
	version int64
	model   string
	cams    map[string]*camera
}

// New builds a simulator.
func New(b *bus.Bus, opts Options) *Simulator {
	if opts.Tick <= 0 {
		opts.Tick = 250 * time.Millisecond
	}
	if opts.MinCooldown <= 0 {
		opts.MinCooldown = 90 * time.Second
	}
	if opts.StatsInterval <= 0 {
		opts.StatsInterval = 10 * time.Second
	}
	if opts.DescDelay <= 0 {
		opts.DescDelay = 1500 * time.Millisecond
	}
	if opts.Seed == 0 {
		opts.Seed = uint64(time.Now().UnixNano())
	}
	return &Simulator{bus: b, opts: opts, rng: rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15)), //nolint:gosec // simulation, not security
		cams: map[string]*camera{}}
}

// Run steps the simulation until ctx is done.
func (s *Simulator) Run(ctx context.Context) {
	slog.Info("simulator started", "tick", s.opts.Tick.String())
	t := time.NewTicker(s.opts.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := s.Step(ctx, now); err != nil && ctx.Err() == nil {
				slog.Warn("simulator step failed", "err", err)
			}
		}
	}
}

// Step advances every camera by one tick.
func (s *Simulator) Step(ctx context.Context, now time.Time) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cams {
		if !c.cfg.Enabled {
			if !c.disabledSent {
				c.disabledSent = true
				s.setStatus(ctx, c, now, "disabled", time.Hour)
			}
			continue
		}
		c.disabledSent = false
		s.stepCamera(ctx, c, now)
	}
	return nil
}

// JPEG renders the latest frame of a camera (nil when the camera is unknown or disabled).
func (s *Simulator) JPEG(cameraID string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cams[cameraID]
	if !ok || !c.cfg.Enabled {
		return nil
	}
	if c.jpeg == nil || c.jpegFrame != c.frame {
		c.jpeg = renderJPEG(c, nil, false)
		c.jpegFrame = c.frame
	}
	return c.jpeg
}

// Interval is the frame interval (for MJPEG streaming).
func (s *Simulator) Interval() time.Duration { return s.opts.Tick }

func (s *Simulator) reload(ctx context.Context) error {
	raw, err := s.bus.R.Get(ctx, bus.ConfigKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc bus.ConfigDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if doc.Version == s.version {
		return nil
	}
	s.version, s.model = doc.Version, doc.ActiveModel
	seen := map[string]bool{}
	for _, cc := range doc.Cameras {
		seen[cc.ID] = true
		if c, ok := s.cams[cc.ID]; ok {
			c.cfg = cc
			continue
		}
		s.cams[cc.ID] = newCamera(cc, s.rng)
	}
	for id := range s.cams {
		if !seen[id] {
			delete(s.cams, id)
		}
	}
	return nil
}

// ---------------------------------------------------------------- camera state

type track struct {
	id             int
	cls            string
	cx, cy, w, h   float64 // normalised centre and size
	tx, ty         float64 // waypoint
	speed          float64 // normalised units per second
	pauseUntil     time.Time
	helmet         bool
	zoneSince      map[string]time.Time
	noHelmetSince  time.Time
	prevAX, prevAY float64
	hasPrev        bool
}

func (t *track) anchor() (float64, float64) { return t.cx, clamp01(t.cy + t.h/2) }

type camera struct {
	cfg          bus.CameraConfig
	rng          *rand.Rand
	tracks       []*track
	nextID       int
	frame        int
	lastStatus   time.Time
	lastStats    time.Time
	window       map[string]int
	points       map[int]store.TrackPoint
	lastAlert    map[string]time.Time
	disabledSent bool
	jpeg         []byte
	jpegFrame    int
	seed         uint64
}

func newCamera(cc bus.CameraConfig, parent *rand.Rand) *camera {
	seed := parent.Uint64()
	c := &camera{cfg: cc, rng: rand.New(rand.NewPCG(seed, seed>>1|1)), nextID: 1, seed: seed, //nolint:gosec // simulation
		window: map[string]int{}, points: map[int]store.TrackPoint{}, lastAlert: map[string]time.Time{}}
	return c
}

func (c *camera) wantsVehicles() bool {
	for _, r := range c.cfg.Rules {
		for _, cls := range r.Params.Classes {
			if cls == "car" || cls == "truck" || cls == "bus" {
				return true
			}
		}
	}
	return len(c.cfg.Rules) == 0 || c.cfg.SourceType == "rtsp"
}

func (c *camera) spawn(cls string) *track {
	t := &track{id: c.nextID, cls: cls, zoneSince: map[string]time.Time{}}
	c.nextID++
	// Enter from a random edge.
	switch c.rng.IntN(4) {
	case 0:
		t.cx, t.cy = 0.03, 0.1+c.rng.Float64()*0.8
	case 1:
		t.cx, t.cy = 0.97, 0.1+c.rng.Float64()*0.8
	case 2:
		t.cx, t.cy = 0.1+c.rng.Float64()*0.8, 0.06
	default:
		t.cx, t.cy = 0.1+c.rng.Float64()*0.8, 0.9
	}
	if cls == "person" {
		t.w, t.h = 0.035+c.rng.Float64()*0.015, 0.11+c.rng.Float64()*0.04
		t.speed = 0.04 + c.rng.Float64()*0.05
		t.helmet = c.rng.Float64() < 0.7
	} else {
		t.w, t.h = 0.12+c.rng.Float64()*0.05, 0.08+c.rng.Float64()*0.03
		t.speed = 0.03 + c.rng.Float64()*0.03
	}
	c.pickWaypoint(t)
	return t
}

// pickWaypoint sends the track either somewhere random or into one of the camera's
// polygon zones, so rules fire naturally rather than on a timer.
func (c *camera) pickWaypoint(t *track) {
	var polys []domain.Zone
	for _, z := range c.cfg.Zones {
		if z.Kind == "polygon" && len(z.Points) >= 3 {
			polys = append(polys, z)
		}
	}
	if len(polys) > 0 && c.rng.Float64() < 0.5 {
		z := polys[c.rng.IntN(len(polys))]
		minX, minY, maxX, maxY := bounds(z.Points)
		for range 10 {
			x := minX + c.rng.Float64()*(maxX-minX)
			y := minY + c.rng.Float64()*(maxY-minY)
			if pointInPolygon(x, y, z.Points) {
				t.tx, t.ty = x, y-t.h/2
				return
			}
		}
	}
	t.tx, t.ty = 0.06+c.rng.Float64()*0.88, 0.08+c.rng.Float64()*0.8
}

func (c *camera) populate() {
	persons, vehicles := 0, 0
	for _, t := range c.tracks {
		if t.cls == "person" {
			persons++
		} else {
			vehicles++
		}
	}
	if persons < 4 && c.rng.Float64() < 0.15 {
		c.tracks = append(c.tracks, c.spawn("person"))
	}
	if c.wantsVehicles() && vehicles < 2 && c.rng.Float64() < 0.05 {
		cls := "car"
		if c.rng.Float64() < 0.3 {
			cls = "truck"
		}
		c.tracks = append(c.tracks, c.spawn(cls))
	}
}

func (c *camera) move(now time.Time, dt float64) {
	kept := c.tracks[:0]
	for _, t := range c.tracks {
		ax, ay := t.anchor()
		t.prevAX, t.prevAY, t.hasPrev = ax, ay, true
		if now.Before(t.pauseUntil) {
			kept = append(kept, t)
			continue
		}
		dx, dy := t.tx-t.cx, t.ty-t.cy
		dist := math.Hypot(dx, dy)
		step := t.speed * dt
		if dist <= step {
			t.cx, t.cy = t.tx, t.ty
			pause := 1 + c.rng.Float64()*4
			if t.cls != "person" {
				pause = 10 + c.rng.Float64()*35
			}
			t.pauseUntil = now.Add(time.Duration(pause * float64(time.Second)))
			c.pickWaypoint(t)
			// Occasionally leave the scene so identities churn.
			if c.rng.Float64() < 0.08 {
				continue
			}
		} else {
			t.cx += dx / dist * step
			t.cy += dy / dist * step
		}
		t.cx = math.Min(math.Max(t.cx, t.w/2), 1-t.w/2)
		t.cy = math.Min(math.Max(t.cy, t.h/2), 1-t.h/2)
		kept = append(kept, t)
	}
	c.tracks = kept
}

// ---------------------------------------------------------------- step

func (s *Simulator) stepCamera(ctx context.Context, c *camera, now time.Time) {
	dt := s.opts.Tick.Seconds()
	c.populate()
	c.move(now, dt)
	c.frame++

	fps := 1 / dt
	latency := 4 + c.rng.Float64()*3
	tracks := make([]map[string]any, 0, len(c.tracks)*2)
	for _, t := range c.tracks {
		tracks = append(tracks, trackMessage(t.id, t.cls, 0.82+c.rng.Float64()*0.15, t.cx-t.w/2, t.cy-t.h/2, t.cx+t.w/2, t.cy+t.h/2))
		if t.cls == "person" && t.helmet {
			hx1, hy1 := t.cx-t.w*0.3, t.cy-t.h/2
			tracks = append(tracks, trackMessage(100000+t.id, "helmet", 0.7+c.rng.Float64()*0.2, hx1, hy1, hx1+t.w*0.6, hy1+t.h*0.18))
		}
	}
	s.publishFrame(ctx, map[string]any{
		"camera_id": c.cfg.ID, "ts": iso(now), "frame": c.frame, "fps": round2(fps), "latency_ms": round2(latency),
		"width": frameW, "height": frameH, "model": s.model, "tracks": tracks,
	})

	s.accumulate(ctx, c, now)
	s.crossings(ctx, c, now)
	s.evaluateRules(ctx, c, now)
	if now.Sub(c.lastStatus) >= 5*time.Second {
		s.setStatus(ctx, c, now, "online", 15*time.Second)
	}
}

func trackMessage(id int, cls string, conf, x1, y1, x2, y2 float64) map[string]any {
	return map[string]any{"id": id, "cls": cls, "conf": round3(conf),
		"bbox": []float64{round4(clamp01(x1)), round4(clamp01(y1)), round4(clamp01(x2)), round4(clamp01(y2))}}
}

func (s *Simulator) accumulate(ctx context.Context, c *camera, now time.Time) {
	counts := map[string]int{}
	for _, t := range c.tracks {
		counts[t.cls]++
		if t.cls == "person" && t.helmet {
			counts["helmet"]++
		}
		if _, ok := c.points[t.id]; !ok {
			x, y := t.anchor()
			c.points[t.id] = store.TrackPoint{TrackID: int32(t.id), Class: t.cls, X: float32(round4(x)), Y: float32(round4(y))} //nolint:gosec // small ids
		}
	}
	for k, n := range counts {
		c.window[k] = max(c.window[k], n)
	}
	if c.lastStats.IsZero() {
		c.lastStats = now
		return
	}
	if now.Sub(c.lastStats) < s.opts.StatsInterval {
		return
	}
	c.lastStats = now
	s.publishEvent(ctx, "stats", map[string]any{"camera_id": c.cfg.ID, "ts": iso(now), "counts": c.window})
	if len(c.points) > 0 {
		pts := make([]store.TrackPoint, 0, len(c.points))
		for _, p := range c.points {
			pts = append(pts, p)
		}
		slices.SortFunc(pts, func(a, b store.TrackPoint) int { return int(a.TrackID - b.TrackID) })
		s.publishEvent(ctx, "track_points", map[string]any{"camera_id": c.cfg.ID, "ts": iso(now), "points": pts})
	}
	c.window = map[string]int{}
	c.points = map[int]store.TrackPoint{}
}

func (s *Simulator) crossings(ctx context.Context, c *camera, now time.Time) {
	for _, z := range c.cfg.Zones {
		if z.Kind != "line" || len(z.Points) < 2 {
			continue
		}
		a, b := z.Points[0], z.Points[1]
		for _, t := range c.tracks {
			if !t.hasPrev {
				continue
			}
			ax, ay := t.anchor()
			dir, ok := segmentCross(t.prevAX, t.prevAY, ax, ay, a, b)
			if !ok {
				continue
			}
			s.publishEvent(ctx, "line_cross", map[string]any{"camera_id": c.cfg.ID, "zone_id": z.ID, "track_id": t.id,
				"cls": t.cls, "direction": dir, "ts": iso(now)})
			for _, r := range c.cfg.Rules {
				if r.Enabled && r.Type == "line_crossing" && r.ZoneID != nil && *r.ZoneID == z.ID && classMatch(r, t.cls, "") {
					s.fire(ctx, c, r, now, []*track{t}, fmt.Sprintf("%s #%d crossed '%s' (%s)", t.cls, t.id, z.Name, dir))
				}
			}
		}
	}
}

func (s *Simulator) evaluateRules(ctx context.Context, c *camera, now time.Time) {
	zones := map[string]domain.Zone{}
	for _, z := range c.cfg.Zones {
		zones[z.ID] = z
	}
	for _, r := range c.cfg.Rules {
		if !r.Enabled {
			continue
		}
		var zone *domain.Zone
		if r.ZoneID != nil {
			if z, ok := zones[*r.ZoneID]; ok && z.Kind == "polygon" {
				zone = &z
			} else if r.Type != "missing_ppe" {
				continue
			}
		}
		inside := func(t *track) bool {
			if zone == nil {
				return true
			}
			x, y := t.anchor()
			return pointInPolygon(x, y, zone.Points)
		}
		zname := "frame"
		if zone != nil {
			zname = "'" + zone.Name + "'"
		}
		switch r.Type {
		case "zone_intrusion":
			for _, t := range c.tracks {
				if classMatch(r, t.cls, "person") && inside(t) {
					s.fire(ctx, c, r, now, []*track{t}, fmt.Sprintf("%s #%d entered restricted zone %s", t.cls, t.id, zname))
					break
				}
			}
		case "dwell_time":
			limit := deref(r.Params.Seconds, 30)
			for _, t := range c.tracks {
				key := r.ID
				if !classMatch(r, t.cls, "") || !inside(t) {
					delete(t.zoneSince, key)
					continue
				}
				since, ok := t.zoneSince[key]
				if !ok {
					t.zoneSince[key] = now
					continue
				}
				if still := now.Sub(since).Seconds(); still >= limit {
					s.fire(ctx, c, r, now, []*track{t},
						fmt.Sprintf("%s #%d stationary for %.0fs in %s (limit %.0fs)", t.cls, t.id, still, zname, limit))
					break
				}
			}
		case "crowd_count":
			limit := 5
			if r.Params.MaxCount != nil {
				limit = *r.Params.MaxCount
			}
			var in []*track
			for _, t := range c.tracks {
				if classMatch(r, t.cls, "person") && inside(t) {
					in = append(in, t)
				}
			}
			if len(in) > limit {
				where := ""
				if zone != nil {
					where = " in " + zone.Name
				}
				s.fire(ctx, c, r, now, in, fmt.Sprintf("Crowd of %d people%s (limit %d)", len(in), where, limit))
			}
		case "missing_ppe":
			limit := deref(r.Params.Seconds, 3)
			for _, t := range c.tracks {
				if t.cls != "person" || t.helmet || !inside(t) {
					t.noHelmetSince = time.Time{}
					continue
				}
				if t.noHelmetSince.IsZero() {
					t.noHelmetSince = now
					continue
				}
				if d := now.Sub(t.noHelmetSince).Seconds(); d >= limit {
					s.fire(ctx, c, r, now, []*track{t}, fmt.Sprintf("worker #%d without helmet for %.0fs", t.id, d))
					break
				}
			}
		}
	}
}

// fire raises an incident unless the rule is cooling down.
func (s *Simulator) fire(ctx context.Context, c *camera, r domain.Rule, now time.Time, ts []*track, message string) {
	cooldown := time.Duration(deref(r.Params.CooldownSeconds, 30) * float64(time.Second))
	cooldown = max(cooldown, s.opts.MinCooldown)
	if last, ok := c.lastAlert[r.ID]; ok && now.Sub(last) < cooldown {
		return
	}
	c.lastAlert[r.ID] = now
	id := uuid.NewString()
	ids := make([]int, len(ts))
	highlight := map[int]bool{}
	for i, t := range ts {
		ids[i] = t.id
		highlight[t.id] = true
	}
	var snapPath *string
	if s.opts.DataDir != "" {
		dir := filepath.Join(s.opts.DataDir, "media", "snapshots")
		if err := os.MkdirAll(dir, 0o750); err == nil {
			if err := os.WriteFile(filepath.Join(dir, id+".jpg"), renderJPEG(c, highlight, true), 0o600); err == nil {
				rel := "snapshots/" + id + ".jpg"
				snapPath = &rel
			}
		}
	}
	s.publishEvent(ctx, "alert", map[string]any{
		"id": id, "camera_id": c.cfg.ID, "rule_id": r.ID, "rule_name": r.Name, "rule_type": r.Type,
		"severity": r.Severity, "occurred_at": iso(now), "track_ids": ids, "object_class": ts[0].cls,
		"message": message, "zone_id": r.ZoneID, "snapshot_path": snapPath, "privacy_applied": c.cfg.PrivacyMode,
	})
	desc := describe(c.cfg.Name, r, ts[0].cls, len(ts), message)
	time.AfterFunc(s.opts.DescDelay, func() {
		s.publishEvent(context.WithoutCancel(ctx), "incident_update", map[string]any{
			"id": id, "description": desc, "description_provider": Provider,
		})
	})
}

func describe(camera string, r domain.Rule, cls string, n int, message string) string {
	subject := "A " + cls
	if n > 1 {
		subject = fmt.Sprintf("A group of %d people", n)
	}
	var detail string
	switch r.Type {
	case "zone_intrusion":
		detail = "is inside an area that is marked as restricted. No authorised escort is visible."
	case "dwell_time":
		detail = "has remained stationary well beyond the allowed time and may be obstructing the area."
	case "crowd_count":
		detail = "has gathered in the monitored zone, exceeding the configured occupancy limit."
	case "missing_ppe":
		detail = "is working on the floor without a visible hard hat while other workers nearby are wearing one."
	case "line_crossing":
		detail = "crossed the counting line."
	default:
		detail = "triggered the rule."
	}
	return fmt.Sprintf("%s on %s %s Rule: %s (%s severity). Detector summary: %s. [simulated description]",
		subject, camera, detail, r.Name, r.Severity, message)
}

func (s *Simulator) setStatus(ctx context.Context, c *camera, now time.Time, state string, ttl time.Duration) {
	c.lastStatus = now
	st := map[string]any{"camera_id": c.cfg.ID, "state": state, "fps": 0.0, "latency_ms": 0.0, "message": nil,
		"model": s.model, "frames": c.frame, "updated_at": iso(now)}
	if state == "online" {
		st["fps"] = round2(1 / s.opts.Tick.Seconds())
		st["latency_ms"] = round2(4 + c.rng.Float64()*3)
		st["message"] = "simulated feed"
	}
	raw, _ := json.Marshal(st)
	if err := s.bus.R.Set(ctx, bus.StatusPrefix+c.cfg.ID, raw, ttl).Err(); err != nil {
		slog.Debug("simulator status failed", "err", err)
	}
	s.publishEvent(ctx, "camera_status", st)
}

func (s *Simulator) publishFrame(ctx context.Context, msg map[string]any) {
	raw, _ := json.Marshal(msg)
	if err := s.bus.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.FramesStream, MaxLen: 200, Approx: true,
		Values: map[string]any{"data": string(raw)}}).Err(); err != nil {
		slog.Debug("simulator frame publish failed", "err", err)
	}
}

func (s *Simulator) publishEvent(ctx context.Context, typ string, data any) {
	raw, _ := json.Marshal(data)
	if err := s.bus.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.EventsStream, MaxLen: 5000, Approx: true,
		Values: map[string]any{"type": typ, "data": string(raw)}}).Err(); err != nil {
		slog.Warn("simulator event publish failed", "type", typ, "err", err)
	}
}

// ---------------------------------------------------------------- geometry helpers

func classMatch(r domain.Rule, cls, def string) bool {
	if len(r.Params.Classes) == 0 {
		return def == "" || cls == def
	}
	return slices.Contains(r.Params.Classes, cls)
}

func pointInPolygon(x, y float64, pts []domain.Point) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		xi, yi, xj, yj := pts[i][0], pts[i][1], pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// segmentCross reports whether p1->p2 crosses segment a-b and in which direction.
func segmentCross(x1, y1, x2, y2 float64, a, b domain.Point) (string, bool) {
	side := func(px, py float64) float64 { return (b[0]-a[0])*(py-a[1]) - (b[1]-a[1])*(px-a[0]) }
	s1, s2 := side(x1, y1), side(x2, y2)
	if s1 == 0 || s2 == 0 || (s1 > 0) == (s2 > 0) {
		return "", false
	}
	// The movement segment must also straddle the line segment's extent.
	m := func(px, py float64) float64 { return (x2-x1)*(py-y1) - (y2-y1)*(px-x1) }
	if (m(a[0], a[1]) > 0) == (m(b[0], b[1]) > 0) {
		return "", false
	}
	if s2 > 0 {
		return "in", true
	}
	return "out", true
}

func bounds(pts []domain.Point) (minX, minY, maxX, maxY float64) {
	minX, minY, maxX, maxY = 1, 1, 0, 0
	for _, p := range pts {
		minX, maxX = math.Min(minX, p[0]), math.Max(maxX, p[0])
		minY, maxY = math.Min(minY, p[1]), math.Max(maxY, p[1])
	}
	return
}

func deref(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func clamp01(v float64) float64 { return math.Min(math.Max(v, 0), 1) }
func round2(v float64) float64  { return math.Round(v*100) / 100 }
func round3(v float64) float64  { return math.Round(v*1000) / 1000 }
func round4(v float64) float64  { return math.Round(v*10000) / 10000 }
func iso(t time.Time) string    { return t.UTC().Format(time.RFC3339Nano) }
