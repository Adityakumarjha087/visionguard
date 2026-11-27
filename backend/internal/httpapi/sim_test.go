package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/httpapi"
	"visionguard/backend/internal/sim"
)

// TestSimulatorEndToEnd drives the in-process simulator against the seeded demo cameras and
// feeds its events through the real ingest processor, as the hosted demo does.
func TestSimulatorEndToEnd(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	require.NoError(t, e.proc.Syncer.Sync(ctx))
	s := sim.New(e.bus, sim.Options{Tick: 250 * time.Millisecond, MinCooldown: time.Second,
		StatsInterval: 2 * time.Second, DescDelay: time.Millisecond, DataDir: e.dataDir, Seed: 7})
	start := time.Now().Add(-3 * time.Minute)
	for i := range 720 {
		require.NoError(t, s.Step(ctx, start.Add(time.Duration(i)*250*time.Millisecond)))
	}
	time.Sleep(50 * time.Millisecond)
	msgs, err := e.bus.R.XRange(ctx, bus.EventsStream, "-", "+").Result()
	require.NoError(t, err)
	for _, m := range msgs {
		ev := bus.Event{ID: m.ID, Type: m.Values["type"].(string), Data: []byte(m.Values["data"].(string))}
		require.NoError(t, e.proc.Handle(ctx, ev))
	}

	viewer := e.tokens[domain.RoleViewer]
	var page domain.Page[domain.Incident]
	e.do(http.MethodGet, "/api/v1/incidents?page_size=100", viewer, nil, http.StatusOK, &page)
	require.Positive(t, page.Total)
	described := 0
	for _, inc := range page.Items {
		if inc.DescriptionProvider != nil && *inc.DescriptionProvider == sim.Provider {
			described++
		}
	}
	assert.Positive(t, described)

	// Stream and snapshot are served in-process when a FrameSource is configured.
	cam := e.warehouse()
	cfg := config.Config{Env: "test", DataDir: e.dataDir, InferenceURL: "http://127.0.0.1:1", CORSOrigins: []string{"*"},
		RateLimitRPS: 1000, RateLimitBurst: 1000, AuthRateLimit: 6000, DefaultModel: "color-synthetic"}
	issuer := auth.NewIssuer("test-secret-with-enough-length", "visionguard", time.Minute, time.Hour)
	api := httpapi.New(cfg, e.st, e.bus, issuer, e.hub, e.proc.Syncer)
	api.Frames = s
	srv := httptest.NewServer(api.Router())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v1/cameras/" + cam.ID + "/snapshot?access_token=" + viewer)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/api/v1/cameras/"+cam.ID+"/stream?access_token="+viewer, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "multipart/x-mixed-replace"))
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "--frame\r\n", line)

	// A frame source that does not know the camera yet -> 503 snapshot.
	api2 := httpapi.New(cfg, e.st, e.bus, issuer, e.hub, e.proc.Syncer)
	api2.Frames = sim.New(e.bus, sim.Options{})
	srv2 := httptest.NewServer(api2.Router())
	t.Cleanup(srv2.Close)
	resp2, err := http.Get(srv2.URL + "/api/v1/cameras/" + cam.ID + "/snapshot?access_token=" + viewer)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp2.StatusCode)
}
