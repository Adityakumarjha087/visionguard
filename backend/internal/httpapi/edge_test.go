package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
)

func TestZoneGeometryEdgeCases(t *testing.T) {
	e := newEnv(t)
	op := e.tokens[domain.RoleOperator]
	path := "/api/v1/cameras/" + e.warehouse().ID + "/zones"
	cases := map[string]map[string]any{
		"two points":       {"name": "z", "kind": "polygon", "points": [][]float64{{0.1, 0.1}, {0.2, 0.2}}},
		"collinear":        {"name": "z", "kind": "polygon", "points": [][]float64{{0.1, 0.1}, {0.2, 0.2}, {0.3, 0.3}}},
		"duplicate points": {"name": "z", "kind": "polygon", "points": [][]float64{{0.1, 0.1}, {0.1, 0.1}, {0.1, 0.1}}},
		"self-intersecting bowtie": {"name": "z", "kind": "polygon",
			"points": [][]float64{{0, 0}, {1, 1}, {1, 0}, {0, 1}}},
		"off-frame":       {"name": "z", "kind": "polygon", "points": [][]float64{{0.5, 0.5}, {1.4, 0.5}, {0.5, 1.2}}},
		"negative":        {"name": "z", "kind": "polygon", "points": [][]float64{{-0.1, 0}, {0.5, 0}, {0.5, 0.5}}},
		"zero-length line": {"name": "z", "kind": "line", "points": [][]float64{{0.3, 0.3}, {0.3, 0.3}}},
		"too many points":  {"name": "z", "kind": "polygon", "points": make([][]float64, 65)},
		"missing name":      {"kind": "polygon", "points": [][]float64{{0, 0}, {1, 0}, {1, 1}}},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { e.do(http.MethodPost, path, op, body, http.StatusUnprocessableEntity, nil) })
	}
	// Concave (non-convex) but simple polygons are fine; edges touching the frame border are allowed.
	e.do(http.MethodPost, path, op, map[string]any{"name": "U", "kind": "polygon",
		"points": [][]float64{{0, 0}, {1, 0}, {1, 1}, {0.6, 1}, {0.6, 0.4}, {0.4, 0.4}, {0.4, 1}, {0, 1}}}, http.StatusCreated, nil)
}

func TestRBACViewerCannotMutateAndNoCrossCameraReferences(t *testing.T) {
	e := newEnv(t)
	viewer, op := e.tokens[domain.RoleViewer], e.tokens[domain.RoleOperator]
	wh := e.warehouse()
	var rules []domain.Rule
	e.do(http.MethodGet, "/api/v1/rules?camera_id="+wh.ID, viewer, nil, http.StatusOK, &rules)
	var zones []domain.Zone
	e.do(http.MethodGet, "/api/v1/cameras/"+wh.ID+"/zones", viewer, nil, http.StatusOK, &zones)
	rule, zone := rules[0], zones[0]

	body := map[string]any{"name": "hijack", "type": "crowd_count"}
	e.do(http.MethodPut, "/api/v1/rules/"+rule.ID, viewer, body, http.StatusForbidden, nil)
	e.do(http.MethodDelete, "/api/v1/rules/"+rule.ID, viewer, nil, http.StatusForbidden, nil)
	e.do(http.MethodPost, "/api/v1/cameras/"+wh.ID+"/rules", viewer, body, http.StatusForbidden, nil)
	e.do(http.MethodPut, "/api/v1/zones/"+zone.ID, viewer, map[string]any{"name": "x", "kind": "line", "points": [][]float64{{0, 0}, {1, 1}}}, http.StatusForbidden, nil)
	e.do(http.MethodDelete, "/api/v1/zones/"+zone.ID, viewer, nil, http.StatusForbidden, nil)
	e.do(http.MethodPatch, "/api/v1/cameras/"+wh.ID, viewer, map[string]any{"enabled": false}, http.StatusForbidden, nil)
	e.do(http.MethodDelete, "/api/v1/cameras/"+wh.ID, viewer, nil, http.StatusForbidden, nil)
	e.do(http.MethodPost, "/api/v1/models/motion-mog2/activate", viewer, nil, http.StatusForbidden, nil)
	assert.Equal(t, http.StatusForbidden, func() int {
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/cameras/upload", strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+viewer)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}())

	// IDOR: a rule on the street camera cannot reference a warehouse zone.
	var cams []domain.Camera
	e.do(http.MethodGet, "/api/v1/cameras", viewer, nil, http.StatusOK, &cams)
	var street domain.Camera
	for _, c := range cams {
		if c.ID != wh.ID {
			street = c
		}
	}
	var details map[string]map[string]any
	e.do(http.MethodPost, "/api/v1/cameras/"+street.ID+"/rules", op,
		map[string]any{"name": "x", "type": "zone_intrusion", "zone_id": zone.ID}, http.StatusUnprocessableEntity, &details)
	assert.Contains(t, details["error"]["details"].(map[string]any)["zone_id"], "not found on this camera")
	// ... nor move an existing rule onto a foreign zone.
	var streetRules []domain.Rule
	e.do(http.MethodGet, "/api/v1/rules?camera_id="+street.ID, op, nil, http.StatusOK, &streetRules)
	e.do(http.MethodPut, "/api/v1/rules/"+streetRules[0].ID, op,
		map[string]any{"name": "x", "type": "zone_intrusion", "zone_id": zone.ID}, http.StatusUnprocessableEntity, nil)
	// Unknown / malformed ids never leak data.
	e.do(http.MethodGet, "/api/v1/incidents/not-a-uuid", viewer, nil, http.StatusNotFound, nil)
	e.do(http.MethodGet, "/api/v1/incidents/00000000-0000-0000-0000-000000000000", viewer, nil, http.StatusNotFound, nil)
	e.do(http.MethodGet, "/api/v1/analytics/heatmap?camera_id=00000000-0000-0000-0000-000000000000", viewer, nil, http.StatusNotFound, nil)
}

func TestAuthRateLimitReturns429(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthRateLimit = 5 })
	// newEnv already spent 3 logins; the bucket holds 5.
	codes := map[int]int{}
	for i := 0; i < 6; i++ {
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/auth/login",
			strings.NewReader(`{"email":"viewer@visionguard.dev","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		codes[resp.StatusCode]++
		if resp.StatusCode == http.StatusTooManyRequests {
			assert.Equal(t, "1", resp.Header.Get("Retry-After"))
		}
	}
	assert.Equal(t, 2, codes[http.StatusUnauthorized])
	assert.Equal(t, 4, codes[http.StatusTooManyRequests])
	// Other endpoints use a separate, larger bucket.
	e.do(http.MethodGet, "/api/v1/cameras", e.tokens[domain.RoleViewer], nil, http.StatusOK, nil)
}

func TestGeneralRateLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RateLimitRPS, c.RateLimitBurst = 0.1, 6 })
	limited := false
	for i := 0; i < 10 && !limited; i++ {
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/cameras", nil)
		req.Header.Set("Authorization", "Bearer "+e.tokens[domain.RoleViewer])
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		limited = resp.StatusCode == http.StatusTooManyRequests
	}
	assert.True(t, limited)
	e.do(http.MethodGet, "/healthz", "", nil, http.StatusOK, nil) // health is never rate limited
}

func TestWebSocketDisconnectAndReconnect(t *testing.T) {
	e := newEnv(t)
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/v1/ws?access_token=" + e.tokens[domain.RoleViewer]
	dial := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(url, nil)
		require.NoError(t, err)
		return c
	}
	first := dial()
	require.NoError(t, first.WriteJSON(map[string]string{"type": "subscribe", "camera_id": "cam"}))
	require.Eventually(t, func() bool { return e.hub.ClientCount() == 1 }, time.Second, 10*time.Millisecond)
	_ = first.Close() // abrupt disconnect
	require.Eventually(t, func() bool { return e.hub.ClientCount() == 0 }, 3*time.Second, 10*time.Millisecond)
	e.hub.Broadcast("alert", map[string]string{"id": "while-offline"}) // no panic on dead client

	second := dial()
	defer second.Close()
	require.NoError(t, second.WriteJSON(map[string]string{"type": "subscribe", "camera_id": "cam"}))
	require.NoError(t, second.WriteJSON(map[string]string{"type": "ping"}))
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	var pong map[string]any
	require.NoError(t, second.ReadJSON(&pong)) // pong arrives after subscribe is processed
	e.hub.PublishFrame("cam", []byte(`{"camera_id":"cam"}`))
	var msg map[string]any
	require.NoError(t, second.ReadJSON(&msg))
	assert.Equal(t, "frame", msg["type"])
}

func TestSlowWebSocketClientDoesNotBlockHub(t *testing.T) {
	e := newEnv(t)
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/v1/ws?access_token=" + e.tokens[domain.RoleViewer]
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "camera_id": "cam"}))
	require.Eventually(t, func() bool { return e.hub.ClientCount() == 1 }, time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	frame := []byte(`{"camera_id":"cam","pad":"` + strings.Repeat("x", 4096) + `"}`)
	start := time.Now()
	for i := 0; i < 20000; i++ { // never read by the client: buffers fill, hub must drop, not block
		e.hub.PublishFrame("cam", frame)
	}
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestInferenceDownKeepsAPIHealthy(t *testing.T) {
	e := newEnv(t)
	e.inference.Close()
	e.do(http.MethodGet, "/readyz", "", nil, http.StatusOK, nil)
	var cams []domain.Camera
	e.do(http.MethodGet, "/api/v1/cameras", e.tokens[domain.RoleViewer], nil, http.StatusOK, &cams)
	for _, c := range cams {
		assert.Equal(t, "offline", c.Status.State) // no status heartbeat -> offline, not an error
	}
	resp, err := http.Get(e.srv.URL + "/api/v1/cameras/" + cams[0].ID + "/stream?access_token=" + e.tokens[domain.RoleViewer])
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestRedisRestartConsumerRecoversAndConfigResyncs(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.bus.ConsumeEvents(ctx, "api-test", e.proc.Handle)
	go e.proc.Syncer.Run(ctx, 100*time.Millisecond)
	wh := e.warehouse()
	add := func() {
		require.NoError(t, e.bus.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.EventsStream, Values: map[string]any{
			"type": "stats", "data": `{"camera_id":"` + wh.ID + `","ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","counts":{"person":2}}`}}).Err())
	}
	countRows := func() int {
		var n int
		require.NoError(t, e.st.Pool().QueryRow(ctx, `SELECT count(*) FROM detection_counts WHERE camera_id = $1`, wh.ID).Scan(&n))
		return n
	}
	time.Sleep(150 * time.Millisecond)
	add()
	require.Eventually(t, func() bool { return countRows() > 0 }, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, e.bus.R.FlushAll(ctx).Err()) // Redis restarted empty: group and config gone
	require.Eventually(t, func() bool { return e.bus.R.Exists(ctx, bus.ConfigKey).Val() == 1 }, 3*time.Second, 20*time.Millisecond)
	before := countRows()
	require.Eventually(t, func() bool {
		add()
		return countRows() > before
	}, 10*time.Second, 200*time.Millisecond)
	pending, err := e.bus.R.XPending(ctx, bus.EventsStream, bus.ConsumerGroup).Result()
	require.NoError(t, err)
	assert.LessOrEqual(t, pending.Count, int64(1)) // nothing stuck
}
