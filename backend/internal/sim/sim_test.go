package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/testutil"
)

func ptr[T any](v T) *T { return &v }

func testDoc() bus.ConfigDoc {
	restricted := domain.Zone{ID: "z-poly", Name: "Everywhere", Kind: "polygon", Color: "#ef4444",
		Points: []domain.Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}}
	counter := domain.Zone{ID: "z-line", Name: "Counter", Kind: "line", Points: []domain.Point{{0.5, 0}, {0.5, 1}}}
	return bus.ConfigDoc{Version: 1, ActiveModel: "color-synthetic", Cameras: []bus.CameraConfig{
		{
			Camera: domain.Camera{ID: "cam-1", Name: "Test cam", SourceType: "file", Enabled: true, PrivacyMode: true, TargetFPS: 10},
			Zones:  []domain.Zone{restricted, counter},
			Rules: []domain.Rule{
				{ID: "r-intrusion", Name: "Intrusion", Type: "zone_intrusion", ZoneID: ptr("z-poly"), Severity: "high", Enabled: true,
					Params: domain.RuleParams{Classes: []string{"person"}}},
				{ID: "r-ppe", Name: "PPE", Type: "missing_ppe", Severity: "high", Enabled: true,
					Params: domain.RuleParams{Seconds: ptr(1.0)}},
				{ID: "r-dwell", Name: "Dwell", Type: "dwell_time", ZoneID: ptr("z-poly"), Severity: "medium", Enabled: true,
					Params: domain.RuleParams{Classes: []string{"car", "truck"}, Seconds: ptr(2.0)}},
			},
		},
		{Camera: domain.Camera{ID: "cam-off", Name: "Off", SourceType: "file", Enabled: false}},
	}}
}

func TestSimulatorPublishesContractEvents(t *testing.T) {
	b, _ := testutil.Redis(t)
	ctx := context.Background()
	require.NoError(t, b.PublishConfig(ctx, testDoc()))
	dir := t.TempDir()
	s := New(b, Options{Tick: 200 * time.Millisecond, MinCooldown: time.Second, StatsInterval: 2 * time.Second,
		DescDelay: time.Millisecond, DataDir: dir, Seed: 42})

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 600 { // two simulated minutes
		require.NoError(t, s.Step(ctx, start.Add(time.Duration(i)*200*time.Millisecond)))
	}
	time.Sleep(50 * time.Millisecond) // let delayed descriptions land

	frames, err := b.R.XLen(ctx, bus.FramesStream).Result()
	require.NoError(t, err)
	assert.Positive(t, frames)

	msgs, err := b.R.XRange(ctx, bus.EventsStream, "-", "+").Result()
	require.NoError(t, err)
	types := map[string]int{}
	var alert map[string]any
	for _, m := range msgs {
		typ := m.Values["type"].(string)
		types[typ]++
		if typ == "alert" && alert == nil {
			require.NoError(t, json.Unmarshal([]byte(m.Values["data"].(string)), &alert))
		}
	}
	for _, want := range []string{"stats", "track_points", "camera_status", "alert", "incident_update"} {
		assert.Positivef(t, types[want], "expected %s events, got %v", want, types)
	}
	require.NotNil(t, alert)
	assert.Equal(t, "cam-1", alert["camera_id"])
	snap, _ := alert["snapshot_path"].(string)
	require.NotEmpty(t, snap)
	_, err = os.Stat(filepath.Join(dir, "media", snap))
	require.NoError(t, err)

	status, err := b.Statuses(ctx, []string{"cam-1", "cam-off"})
	require.NoError(t, err)
	assert.Equal(t, "online", status["cam-1"].State)
	assert.Equal(t, "disabled", status["cam-off"].State)

	jpg := s.JPEG("cam-1")
	require.NotEmpty(t, jpg)
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	require.NoError(t, err)
	assert.Equal(t, frameW, img.Bounds().Dx())
	assert.Nil(t, s.JPEG("cam-off"))
	assert.Nil(t, s.JPEG("missing"))

	// Removing a camera from the desired state drops it.
	doc := testDoc()
	doc.Version, doc.Cameras = 2, doc.Cameras[:1]
	require.NoError(t, b.PublishConfig(ctx, doc))
	require.NoError(t, s.Step(ctx, start.Add(time.Hour)))
	s.mu.Lock()
	_, ok := s.cams["cam-off"]
	s.mu.Unlock()
	assert.False(t, ok)
}

func TestSimulatorWithoutConfigAndRun(t *testing.T) {
	b, _ := testutil.Redis(t)
	s := New(b, Options{Tick: 10 * time.Millisecond})
	assert.Equal(t, 10*time.Millisecond, s.Interval())
	require.NoError(t, s.Step(context.Background(), time.Now()))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.Run(ctx) // returns on cancellation

	require.NoError(t, b.R.Set(context.Background(), bus.ConfigKey, "{bad", 0).Err())
	assert.Error(t, s.Step(context.Background(), time.Now()))
}

func TestGeometry(t *testing.T) {
	sq := []domain.Point{{0.2, 0.2}, {0.8, 0.2}, {0.8, 0.8}, {0.2, 0.8}}
	assert.True(t, pointInPolygon(0.5, 0.5, sq))
	assert.False(t, pointInPolygon(0.1, 0.5, sq))

	line := [2]domain.Point{{0.5, 0}, {0.5, 1}}
	dir, ok := segmentCross(0.4, 0.5, 0.6, 0.5, line[0], line[1])
	assert.True(t, ok)
	back, ok2 := segmentCross(0.6, 0.5, 0.4, 0.5, line[0], line[1])
	assert.True(t, ok2)
	assert.NotEqual(t, dir, back)
	_, ok = segmentCross(0.1, 0.5, 0.3, 0.5, line[0], line[1])
	assert.False(t, ok)
	short := [2]domain.Point{{0.5, 0}, {0.5, 0.2}}
	_, ok = segmentCross(0.4, 0.5, 0.6, 0.5, short[0], short[1])
	assert.False(t, ok, "movement passes beside the segment")

	assert.Equal(t, uint8(0xef), parseHex("#ef4444", boxColor).R)
	assert.Equal(t, boxColor, parseHex("nope", boxColor))
	assert.Equal(t, boxColor, parseHex("#zzzzzz", boxColor))
}

func TestDescribeCoversRuleTypes(t *testing.T) {
	for _, typ := range []string{"zone_intrusion", "dwell_time", "crowd_count", "missing_ppe", "line_crossing", "other"} {
		d := describe("Cam", domain.Rule{Name: "R", Type: typ, Severity: "low"}, "person", 2, "msg")
		assert.Contains(t, d, "simulated")
	}
}
