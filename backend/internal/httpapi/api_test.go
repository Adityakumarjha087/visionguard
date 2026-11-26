package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/httpapi"
	"visionguard/backend/internal/ingest"
	"visionguard/backend/internal/seed"
	"visionguard/backend/internal/store"
	"visionguard/backend/internal/testutil"
	"visionguard/backend/internal/ws"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup(context.Background())
	os.Exit(code)
}

type env struct {
	t         *testing.T
	srv       *httptest.Server
	st        *store.Store
	bus       *bus.Bus
	hub       *ws.Hub
	proc      *ingest.Processor
	dataDir   string
	inference *httptest.Server
	tokens    map[domain.Role]string
}

type recordingNotifier struct{ got []domain.Incident }

func (r *recordingNotifier) Notify(inc domain.Incident) { r.got = append(r.got, inc) }

func newEnv(t *testing.T) *env {
	t.Helper()
	st := testutil.Store(t)
	testutil.Reset(t, st)
	b, _ := testutil.Redis(t)
	dataDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "videos", "samples"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "videos", "samples", "warehouse.mp4"), []byte("fake"), 0o644))

	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/snapshot/"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
		case strings.HasPrefix(r.URL.Path, "/stream/"):
			w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=vgframe")
			_, _ = w.Write([]byte("--vgframe\r\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(inference.Close)

	cfg := config.Config{
		Env: "test", DataDir: dataDir, InferenceURL: inference.URL, CORSOrigins: []string{"http://localhost:20900"},
		RateLimitRPS: 1000, RateLimitBurst: 1000, AuthRateLimit: 6000, MaxUploadBytes: 1 << 20, DefaultModel: "color-synthetic",
	}
	issuer := auth.NewIssuer("test-secret-with-enough-length", "visionguard", time.Minute, time.Hour)
	hub := ws.NewHub([]string{"*"})
	syncer := &ingest.Syncer{Store: st, Bus: b, DefaultModel: cfg.DefaultModel}
	srv := httptest.NewServer(httpapi.New(cfg, st, b, issuer, hub, syncer).Router())
	t.Cleanup(srv.Close)
	e := &env{t: t, srv: srv, st: st, bus: b, hub: hub, dataDir: dataDir, inference: inference,
		proc:   &ingest.Processor{Store: st, Hub: hub, Notifier: &recordingNotifier{}, Syncer: syncer},
		tokens: map[domain.Role]string{}}
	require.NoError(t, seed.Run(context.Background(), st, "", 0))
	for _, u := range seed.DemoUsers {
		var resp map[string]any
		e.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": u.Email, "password": u.Password}, http.StatusOK, &resp)
		e.tokens[u.Role] = resp["access_token"].(string)
	}
	return e
}

// do performs a JSON request and asserts the status; out may be nil.
func (e *env) do(method, path, token string, body any, want int, out any) *http.Response {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(e.t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(e.t, want, resp.StatusCode, "%s %s -> %s", method, path, raw)
	if out != nil && len(raw) > 0 {
		require.NoError(e.t, json.Unmarshal(raw, out))
	}
	return resp
}

func (e *env) warehouse() domain.Camera {
	var cams []domain.Camera
	e.do(http.MethodGet, "/api/v1/cameras", e.tokens[domain.RoleViewer], nil, http.StatusOK, &cams)
	for _, c := range cams {
		if c.Name == seed.WarehouseCamera {
			return c
		}
	}
	e.t.Fatal("warehouse camera not seeded")
	return domain.Camera{}
}

func TestHealthDocsAndMetrics(t *testing.T) {
	e := newEnv(t)
	e.do(http.MethodGet, "/healthz", "", nil, http.StatusOK, nil)
	var ready map[string]any
	e.do(http.MethodGet, "/readyz", "", nil, http.StatusOK, &ready)
	assert.Equal(t, "ok", ready["status"])
	e.do(http.MethodGet, "/api/docs", "", nil, http.StatusOK, nil)
	e.do(http.MethodGet, "/api/docs/openapi.yaml", "", nil, http.StatusOK, nil)
	resp, err := http.Get(e.srv.URL + "/metrics")
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Contains(t, string(raw), "vg_http_requests_total")
	var apiErr map[string]map[string]any
	e.do(http.MethodGet, "/nope", "", nil, http.StatusNotFound, &apiErr)
	assert.Equal(t, "not_found", apiErr["error"]["code"])
}

func TestAuthFlowWithRefreshRotationAndReuseDetection(t *testing.T) {
	e := newEnv(t)
	var reg map[string]any
	e.do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "New@Example.com", "password": "password1", "name": "New"}, http.StatusCreated, &reg)
	assert.Equal(t, "viewer", reg["user"].(map[string]any)["role"])
	assert.Equal(t, "new@example.com", reg["user"].(map[string]any)["email"])
	e.do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "new@example.com", "password": "password1", "name": "Dup"}, http.StatusConflict, nil)

	var verr map[string]map[string]any
	e.do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "bad", "password": "x"}, http.StatusUnprocessableEntity, &verr)
	assert.Equal(t, "validation_error", verr["error"]["code"])
	e.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "new@example.com", "password": "wrong-pass"}, http.StatusUnauthorized, nil)
	e.do(http.MethodPost, "/api/v1/auth/login", "", "not-json", http.StatusBadRequest, nil)

	var me domain.User
	e.do(http.MethodGet, "/api/v1/auth/me", reg["access_token"].(string), nil, http.StatusOK, &me)
	assert.Equal(t, "New", me.Name)
	e.do(http.MethodGet, "/api/v1/auth/me", "garbage", nil, http.StatusUnauthorized, nil)
	e.do(http.MethodGet, "/api/v1/auth/me", "", nil, http.StatusUnauthorized, nil)

	first := reg["refresh_token"].(string)
	var rotated map[string]any
	e.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first}, http.StatusOK, &rotated)
	second := rotated["refresh_token"].(string)
	assert.NotEqual(t, first, second)
	// Reusing the rotated token revokes the whole family.
	e.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first}, http.StatusUnauthorized, nil)
	e.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": second}, http.StatusUnauthorized, nil)
	e.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": "unknown"}, http.StatusUnauthorized, nil)

	var again map[string]any
	e.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "new@example.com", "password": "password1"}, http.StatusOK, &again)
	e.do(http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": again["refresh_token"].(string)}, http.StatusNoContent, nil)
	e.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": again["refresh_token"].(string)}, http.StatusUnauthorized, nil)
}

func TestRBACAndUserManagement(t *testing.T) {
	e := newEnv(t)
	viewer, operator, admin := e.tokens[domain.RoleViewer], e.tokens[domain.RoleOperator], e.tokens[domain.RoleAdmin]
	body := map[string]any{"name": "Gate", "source_type": "rtsp", "source_uri": "rtsp://cam.local/stream"}
	e.do(http.MethodPost, "/api/v1/cameras", viewer, body, http.StatusForbidden, nil)
	e.do(http.MethodPost, "/api/v1/cameras", operator, body, http.StatusCreated, nil)
	e.do(http.MethodGet, "/api/v1/users", operator, nil, http.StatusForbidden, nil)

	var users []domain.User
	e.do(http.MethodGet, "/api/v1/users", admin, nil, http.StatusOK, &users)
	require.Len(t, users, 3)
	var viewerUser, adminUser domain.User
	for _, u := range users {
		switch u.Role {
		case domain.RoleViewer:
			viewerUser = u
		case domain.RoleAdmin:
			adminUser = u
		}
	}
	var updated domain.User
	e.do(http.MethodPatch, "/api/v1/users/"+viewerUser.ID, admin, map[string]string{"role": "operator"}, http.StatusOK, &updated)
	assert.Equal(t, domain.RoleOperator, updated.Role)
	e.do(http.MethodPatch, "/api/v1/users/"+viewerUser.ID, admin, map[string]string{"role": "root"}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPatch, "/api/v1/users/"+adminUser.ID, admin, map[string]string{"role": "viewer"}, http.StatusConflict, nil)
	e.do(http.MethodDelete, "/api/v1/users/"+adminUser.ID, admin, nil, http.StatusConflict, nil)
	e.do(http.MethodDelete, "/api/v1/users/"+viewerUser.ID, admin, nil, http.StatusNoContent, nil)
	e.do(http.MethodDelete, "/api/v1/users/"+viewerUser.ID, admin, nil, http.StatusNotFound, nil)
}

func TestCameraLifecycleAndConfigSync(t *testing.T) {
	e := newEnv(t)
	op := e.tokens[domain.RoleOperator]
	e.do(http.MethodPost, "/api/v1/cameras", op, map[string]any{"name": "X", "source_type": "file", "source_uri": "samples/missing.mp4"}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, "/api/v1/cameras", op, map[string]any{"name": "X", "source_type": "file", "source_uri": "../../etc/passwd"}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, "/api/v1/cameras", op, map[string]any{"name": "X", "source_type": "rtsp", "source_uri": "ftp://x"}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, "/api/v1/cameras", op, map[string]any{"name": "X", "source_type": "tv", "source_uri": "1"}, http.StatusUnprocessableEntity, nil)

	var cam domain.Camera
	e.do(http.MethodPost, "/api/v1/cameras", op, map[string]any{"name": "Dock", "source_type": "file",
		"source_uri": "samples/warehouse.mp4", "target_fps": 5, "privacy_mode": false}, http.StatusCreated, &cam)
	assert.Equal(t, 5.0, cam.TargetFPS)
	assert.False(t, cam.PrivacyMode)
	assert.Equal(t, "offline", cam.Status.State)

	// Live status comes from the inference service via Redis.
	require.NoError(t, e.bus.R.Set(context.Background(), bus.StatusPrefix+cam.ID, `{"state":"online","fps":9.8,"latency_ms":3.1}`, 0).Err())
	var got domain.Camera
	e.do(http.MethodGet, "/api/v1/cameras/"+cam.ID, e.tokens[domain.RoleViewer], nil, http.StatusOK, &got)
	assert.Equal(t, "online", got.Status.State)
	assert.InDelta(t, 9.8, got.Status.FPS, 0.01)

	var patched domain.Camera
	e.do(http.MethodPatch, "/api/v1/cameras/"+cam.ID, op, map[string]any{"enabled": false, "name": "Dock 2"}, http.StatusOK, &patched)
	assert.Equal(t, "disabled", patched.Status.State)
	e.do(http.MethodPatch, "/api/v1/cameras/"+cam.ID, op, map[string]any{"source_uri": "samples/none.mp4"}, http.StatusUnprocessableEntity, nil)

	raw, err := e.bus.R.Get(context.Background(), bus.ConfigKey).Bytes()
	require.NoError(t, err)
	var doc bus.ConfigDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, "color-synthetic", doc.ActiveModel)
	assert.Len(t, doc.Cameras, 3)
	for _, c := range doc.Cameras {
		if c.Name == seed.WarehouseCamera {
			assert.Len(t, c.Zones, 4)
			assert.Len(t, c.Rules, 4)
		}
	}

	var samples []map[string]any
	e.do(http.MethodGet, "/api/v1/samples", op, nil, http.StatusOK, &samples)
	require.Len(t, samples, 1)
	assert.Equal(t, "samples/warehouse.mp4", samples[0]["source_uri"])

	e.do(http.MethodDelete, "/api/v1/cameras/"+cam.ID, op, nil, http.StatusNoContent, nil)
	e.do(http.MethodGet, "/api/v1/cameras/"+cam.ID, op, nil, http.StatusNotFound, nil)
	e.do(http.MethodGet, "/api/v1/cameras/not-a-uuid", op, nil, http.StatusNotFound, nil)
}

func upload(t *testing.T, e *env, filename string, content []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, _ = fw.Write(content)
	_ = mw.WriteField("name", "Uploaded clip")
	require.NoError(t, mw.Close())
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/cameras/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.tokens[domain.RoleOperator])
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestUploadVideo(t *testing.T) {
	e := newEnv(t)
	resp := upload(t, e, "clip.mp4", []byte("not really a video"))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var cam domain.Camera
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cam))
	assert.Equal(t, "file", cam.SourceType)
	assert.True(t, strings.HasPrefix(cam.SourceURI, "uploads/"))
	_, err := os.Stat(filepath.Join(e.dataDir, "videos", cam.SourceURI))
	assert.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, upload(t, e, "notes.txt", []byte("x")).StatusCode)
	assert.Equal(t, http.StatusRequestEntityTooLarge, upload(t, e, "big.mp4", make([]byte, 3<<20)).StatusCode)
}

func TestZonesAndRulesValidation(t *testing.T) {
	e := newEnv(t)
	op := e.tokens[domain.RoleOperator]
	cam := e.warehouse()
	base := "/api/v1/cameras/" + cam.ID

	e.do(http.MethodPost, base+"/zones", op, map[string]any{"name": "Tri", "kind": "polygon", "points": [][]float64{{0, 0}, {1, 0}}}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/zones", op, map[string]any{"name": "L", "kind": "line", "points": [][]float64{{0, 0}, {2, 0}}}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/zones", op, map[string]any{"name": "L", "kind": "line", "points": [][]float64{{0, 0}, {1, 1}}, "color": "red"}, http.StatusUnprocessableEntity, nil)
	var poly, line domain.Zone
	e.do(http.MethodPost, base+"/zones", op, map[string]any{"name": "Gate", "kind": "polygon", "points": [][]float64{{0.1, 0.1}, {0.3, 0.1}, {0.2, 0.4}}}, http.StatusCreated, &poly)
	assert.Equal(t, "#22c55e", poly.Color)
	e.do(http.MethodPost, base+"/zones", op, map[string]any{"name": "Door", "kind": "line", "points": [][]float64{{0.2, 0}, {0.2, 1}}, "color": "#112233"}, http.StatusCreated, &line)
	e.do(http.MethodPut, "/api/v1/zones/"+poly.ID, op, map[string]any{"name": "Gate 2", "kind": "polygon", "points": [][]float64{{0.1, 0.1}, {0.3, 0.1}, {0.2, 0.5}}, "color": "#ff0000"}, http.StatusOK, &poly)
	assert.Equal(t, "Gate 2", poly.Name)
	var zones []domain.Zone
	e.do(http.MethodGet, base+"/zones", e.tokens[domain.RoleViewer], nil, http.StatusOK, &zones)
	assert.Len(t, zones, 6)

	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "R", "type": "zone_intrusion"}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "R", "type": "line_crossing", "zone_id": poly.ID}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "R", "type": "crowd_count", "zone_id": line.ID}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "R", "type": "crowd_count", "params": map[string]any{"max_count": -1}}, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "R", "type": "teleport"}, http.StatusUnprocessableEntity, nil)

	var rule domain.Rule
	e.do(http.MethodPost, base+"/rules", op, map[string]any{"name": "Door count", "type": "line_crossing", "zone_id": line.ID,
		"params": map[string]any{"classes": []string{"person"}}, "notify_email": true}, http.StatusCreated, &rule)
	assert.Equal(t, "medium", rule.Severity)
	assert.True(t, rule.Enabled && rule.NotifyEmail)
	e.do(http.MethodPut, "/api/v1/rules/"+rule.ID, op, map[string]any{"name": "Door count", "type": "line_crossing", "zone_id": line.ID,
		"severity": "critical", "enabled": false}, http.StatusOK, &rule)
	assert.Equal(t, "critical", rule.Severity)
	assert.False(t, rule.Enabled)

	var rules []domain.Rule
	e.do(http.MethodGet, "/api/v1/rules?camera_id="+cam.ID, op, nil, http.StatusOK, &rules)
	assert.Len(t, rules, 5)
	e.do(http.MethodDelete, "/api/v1/zones/"+line.ID, op, nil, http.StatusNoContent, nil) // cascades to the rule
	e.do(http.MethodPut, "/api/v1/rules/"+rule.ID, op, map[string]any{"name": "x", "type": "crowd_count"}, http.StatusNotFound, nil)
	e.do(http.MethodDelete, "/api/v1/rules/"+rule.ID, op, nil, http.StatusNotFound, nil)
	e.do(http.MethodDelete, "/api/v1/zones/"+line.ID, op, nil, http.StatusNotFound, nil)
	e.do(http.MethodGet, "/api/v1/cameras/00000000-0000-0000-0000-000000000000/zones", op, nil, http.StatusNotFound, nil)
}

func ingestEvent(t *testing.T, e *env, typ string, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, e.proc.Handle(context.Background(), bus.Event{ID: "1-0", Type: typ, Data: raw}))
}

func TestIngestIncidentsAnalyticsAndSearch(t *testing.T) {
	e := newEnv(t)
	cam := e.warehouse()
	var rules []domain.Rule
	e.do(http.MethodGet, "/api/v1/rules?camera_id="+cam.ID, e.tokens[domain.RoleViewer], nil, http.StatusOK, &rules)
	var intrusion domain.Rule
	for _, r := range rules {
		if r.Type == "zone_intrusion" {
			intrusion = r
		}
	}
	now := time.Now().UTC()
	incidentID := "6f1c1c55-7d7b-4c43-9a59-2a3f3c9f0001"
	alert := map[string]any{"id": incidentID, "camera_id": cam.ID, "rule_id": intrusion.ID, "rule_name": intrusion.Name,
		"rule_type": "zone_intrusion", "severity": "high", "occurred_at": now.Format(time.RFC3339Nano), "track_ids": []int{7},
		"object_class": "person", "message": "person #7 entered restricted zone 'Restricted area'", "snapshot_path": "snapshots/" + incidentID + ".jpg"}
	ingestEvent(t, e, "alert", alert)
	ingestEvent(t, e, "alert", alert) // duplicate delivery is idempotent
	notifier := e.proc.Notifier.(*recordingNotifier)
	require.Len(t, notifier.got, 1)
	assert.True(t, notifier.got[0].NotifyEmail)

	ingestEvent(t, e, "incident_update", map[string]any{"id": incidentID, "description": "A forklift driver entered the cage.", "description_provider": "fake"})
	ingestEvent(t, e, "incident_update", map[string]any{"id": incidentID, "clip_path": "clips/" + incidentID + ".mp4"})
	ingestEvent(t, e, "incident_update", map[string]any{"id": "6f1c1c55-7d7b-4c43-9a59-2a3f3c9f0999", "clip_path": "x"}) // unknown -> dropped
	ingestEvent(t, e, "alert", map[string]any{"id": "6f1c1c55-7d7b-4c43-9a59-2a3f3c9f0002", "camera_id": "00000000-0000-0000-0000-000000000000",
		"rule_name": "x", "rule_type": "crowd_count", "severity": "low", "occurred_at": now.Format(time.RFC3339), "message": "orphan"})
	require.NoError(t, e.proc.Handle(context.Background(), bus.Event{Type: "alert", Data: []byte("{bad")}))

	for i := 0; i < 5; i++ {
		ts := now.Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		ingestEvent(t, e, "stats", map[string]any{"camera_id": cam.ID, "ts": ts, "counts": map[string]int{"person": 3 + i, "car": 1, "helmet": 2}})
		ingestEvent(t, e, "track_points", map[string]any{"camera_id": cam.ID, "ts": ts,
			"points": []map[string]any{{"track_id": 1, "cls": "person", "x": 0.1, "y": 0.1}, {"track_id": 2, "cls": "car", "x": 0.99, "y": 0.99}}})
	}
	var lineID string
	var zones []domain.Zone
	e.do(http.MethodGet, "/api/v1/cameras/"+cam.ID+"/zones", e.tokens[domain.RoleViewer], nil, http.StatusOK, &zones)
	for _, z := range zones {
		if z.Kind == "line" {
			lineID = z.ID
		}
	}
	for _, dir := range []string{"in", "in", "out"} {
		ingestEvent(t, e, "line_cross", map[string]any{"camera_id": cam.ID, "zone_id": lineID, "track_id": 4, "cls": "person", "direction": dir, "ts": now.Format(time.RFC3339Nano)})
	}
	ingestEvent(t, e, "camera_status", map[string]any{"camera_id": cam.ID, "state": "online"})
	ingestEvent(t, e, "model_catalog", map[string]any{"models": []map[string]any{{"id": "yolo11n-onnx", "name": "YOLO11n ONNX", "backend": "onnx",
		"classes": []string{"person"}, "available": true, "latency_ms": 21.5, "fps": 46.5}}})
	ingestEvent(t, e, "unknown_type", map[string]any{})

	viewer := e.tokens[domain.RoleViewer]
	var inc domain.Incident
	e.do(http.MethodGet, "/api/v1/incidents/"+incidentID, viewer, nil, http.StatusOK, &inc)
	assert.Equal(t, "A forklift driver entered the cage.", *inc.Description)
	assert.Equal(t, "/api/v1/media/clips/"+incidentID+".mp4", *inc.ClipURL)
	assert.Equal(t, seed.WarehouseCamera, inc.CameraName)

	var page domain.Page[domain.Incident]
	e.do(http.MethodGet, "/api/v1/incidents?q=forklift", viewer, nil, http.StatusOK, &page)
	assert.Equal(t, 1, page.Total)
	e.do(http.MethodGet, "/api/v1/incidents?severity=high&status=open&rule_type=zone_intrusion&camera_id="+cam.ID+
		"&from="+now.Add(-time.Minute).Format(time.RFC3339)+"&page_size=5", viewer, nil, http.StatusOK, &page)
	assert.Equal(t, 1, page.Total)
	assert.Equal(t, 5, page.PageSize)
	e.do(http.MethodGet, "/api/v1/incidents?severity=apocalyptic", viewer, nil, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodGet, "/api/v1/incidents?from=yesterday", viewer, nil, http.StatusUnprocessableEntity, nil)

	e.do(http.MethodPatch, "/api/v1/incidents/"+incidentID, viewer, map[string]string{"status": "resolved"}, http.StatusForbidden, nil)
	e.do(http.MethodPatch, "/api/v1/incidents/"+incidentID, e.tokens[domain.RoleOperator], map[string]string{"status": "acknowledged"}, http.StatusOK, &inc)
	assert.Equal(t, "acknowledged", inc.Status)
	assert.NotNil(t, inc.AcknowledgedBy)
	e.do(http.MethodPatch, "/api/v1/incidents/"+incidentID, e.tokens[domain.RoleOperator], map[string]string{"status": "open"}, http.StatusOK, &inc)
	assert.Nil(t, inc.AcknowledgedBy)
	e.do(http.MethodPatch, "/api/v1/incidents/6f1c1c55-7d7b-4c43-9a59-2a3f3c9f0abc", e.tokens[domain.RoleOperator], map[string]string{"status": "open"}, http.StatusNotFound, nil)

	var sum domain.Summary
	e.do(http.MethodGet, "/api/v1/analytics/summary", viewer, nil, http.StatusOK, &sum)
	assert.Equal(t, 2, sum.CamerasTotal)
	assert.Equal(t, 3, sum.PeopleNow)
	assert.Equal(t, 1, sum.VehiclesNow)
	assert.Equal(t, 2, sum.CrossingsIn24h)
	assert.Equal(t, 1, sum.CrossingsOut24h)
	assert.Equal(t, 1, sum.OpenIncidents)

	var counts []domain.CountBucket
	e.do(http.MethodGet, "/api/v1/analytics/counts?bucket=1h&camera_id="+cam.ID, viewer, nil, http.StatusOK, &counts)
	require.NotEmpty(t, counts)
	classes := map[string]int{}
	for _, c := range counts {
		classes[c.ObjectClass] = max(classes[c.ObjectClass], c.MaxCount)
	}
	assert.Equal(t, 7, classes["person"])
	assert.NotContains(t, classes, "helmet")
	e.do(http.MethodGet, "/api/v1/analytics/counts?bucket=7m", viewer, nil, http.StatusUnprocessableEntity, nil)
	e.do(http.MethodGet, "/api/v1/analytics/counts?from="+now.Format(time.RFC3339)+"&to="+now.Add(-time.Hour).Format(time.RFC3339), viewer, nil, http.StatusUnprocessableEntity, nil)

	var hm domain.Heatmap
	e.do(http.MethodGet, "/api/v1/analytics/heatmap?grid=16&camera_id="+cam.ID, viewer, nil, http.StatusOK, &hm)
	assert.Equal(t, 16, hm.Cols)
	assert.Equal(t, 9, hm.Rows)
	assert.Equal(t, 5, hm.Cells[0*16+1])          // (0.1, 0.1): col 1, row 0
	assert.Equal(t, 5, hm.Cells[len(hm.Cells)-1]) // (0.99, 0.99) clamps to the last cell
	e.do(http.MethodGet, "/api/v1/analytics/heatmap", viewer, nil, http.StatusUnprocessableEntity, nil)

	var crossings []domain.CrossingBucket
	e.do(http.MethodGet, "/api/v1/analytics/line-crossings?camera_id="+cam.ID, viewer, nil, http.StatusOK, &crossings)
	total := 0
	for _, c := range crossings {
		total += c.Count
		assert.Equal(t, "Aisle counter", c.ZoneName)
	}
	assert.Equal(t, 3, total)

	var models []domain.Model
	e.do(http.MethodGet, "/api/v1/models", viewer, nil, http.StatusOK, &models)
	byID := map[string]domain.Model{}
	for _, m := range models {
		byID[m.ID] = m
	}
	assert.True(t, byID["color-synthetic"].Active)
	assert.True(t, byID["yolo11n-onnx"].Available)
	assert.InDelta(t, 21.5, *byID["yolo11n-onnx"].LatencyMS, 0.01)
}

func TestModelActivation(t *testing.T) {
	e := newEnv(t)
	admin := e.tokens[domain.RoleAdmin]
	e.do(http.MethodPost, "/api/v1/models/yolo11n/activate", e.tokens[domain.RoleOperator], nil, http.StatusForbidden, nil)
	e.do(http.MethodPost, "/api/v1/models/yolo11n/activate", admin, nil, http.StatusConflict, nil) // weights absent
	e.do(http.MethodPost, "/api/v1/models/does-not-exist/activate", admin, nil, http.StatusNotFound, nil)
	// The inference service advertises a live catalogue in Redis.
	require.NoError(t, e.bus.R.Set(context.Background(), bus.ModelsKey,
		`[{"id":"motion-mog2","name":"MOG2","backend":"motion","classes":["object"],"available":true}]`, 0).Err())
	var m domain.Model
	e.do(http.MethodPost, "/api/v1/models/motion-mog2/activate", admin, nil, http.StatusOK, &m)
	assert.True(t, m.Active)
	raw, _ := e.bus.R.Get(context.Background(), bus.ConfigKey).Bytes()
	var doc bus.ConfigDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, "motion-mog2", doc.ActiveModel)
}

func TestMediaStreamProxyAndQueryTokenAuth(t *testing.T) {
	e := newEnv(t)
	cam := e.warehouse()
	token := e.tokens[domain.RoleViewer]
	snapDir := filepath.Join(e.dataDir, "media", "snapshots")
	require.NoError(t, os.MkdirAll(snapDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "abc-1.jpg"), []byte{0xff, 0xd8}, 0o644))

	get := func(path string) *http.Response {
		resp, err := http.Get(e.srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		return resp
	}
	assert.Equal(t, http.StatusOK, get("/api/v1/media/snapshots/abc-1.jpg?access_token="+token).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, get("/api/v1/media/snapshots/abc-1.jpg").StatusCode)
	assert.Equal(t, http.StatusNotFound, get("/api/v1/media/snapshots/..%2F..%2Fsecret.jpg?access_token="+token).StatusCode)
	assert.Equal(t, http.StatusNotFound, get("/api/v1/media/other/abc-1.jpg?access_token="+token).StatusCode)
	assert.Equal(t, http.StatusNotFound, get("/api/v1/media/clips/missing.mp4?access_token="+token).StatusCode)

	snap := get("/api/v1/cameras/" + cam.ID + "/snapshot?access_token=" + token)
	assert.Equal(t, http.StatusOK, snap.StatusCode)
	assert.Equal(t, "image/jpeg", snap.Header.Get("Content-Type"))
	stream := get("/api/v1/cameras/" + cam.ID + "/stream?access_token=" + token)
	assert.Contains(t, stream.Header.Get("Content-Type"), "multipart/x-mixed-replace")
	assert.Equal(t, http.StatusNotFound, get("/api/v1/cameras/bogus/stream?access_token="+token).StatusCode)
	// Query tokens are not accepted on regular endpoints.
	assert.Equal(t, http.StatusUnauthorized, get("/api/v1/cameras?access_token="+token).StatusCode)

	e.inference.Close()
	assert.Equal(t, http.StatusBadGateway, get("/api/v1/cameras/"+cam.ID+"/snapshot?access_token="+token).StatusCode)
}

func TestWebSocketSubscriptionsAndBroadcast(t *testing.T) {
	e := newEnv(t)
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/v1/ws?access_token=" + e.tokens[domain.RoleViewer]
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "camera_id": "cam-a"}))
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "ping"}))

	read := func() map[string]any {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var msg map[string]any
		require.NoError(t, conn.ReadJSON(&msg))
		return msg
	}
	assert.Equal(t, "pong", read()["type"])
	require.Eventually(t, func() bool { return e.hub.ClientCount() == 1 }, time.Second, 10*time.Millisecond)
	e.hub.PublishFrame("cam-b", []byte(`{"camera_id":"cam-b"}`)) // not subscribed: filtered out
	e.hub.PublishFrame("cam-a", []byte(`{"camera_id":"cam-a","fps":10}`))
	frame := read()
	assert.Equal(t, "frame", frame["type"])
	assert.Equal(t, "cam-a", frame["data"].(map[string]any)["camera_id"])
	e.hub.Broadcast("alert", map[string]string{"id": "x"})
	assert.Equal(t, "alert", read()["type"])

	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+"/api/v1/ws", nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestCORSAndRateLimit(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodOptions, e.srv.URL+"/api/v1/cameras", nil)
	req.Header.Set("Origin", "http://localhost:20900")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "http://localhost:20900", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Authorization")

	rl := httpapi.NewRateLimiter(1, 2)
	assert.True(t, rl.Allow("a"))
	assert.True(t, rl.Allow("a"))
	assert.False(t, rl.Allow("a"))
	assert.True(t, rl.Allow("b"))
}

func TestSeedIsIdempotent(t *testing.T) {
	e := newEnv(t)
	bench := filepath.Join(t.TempDir(), "b.json")
	require.NoError(t, os.WriteFile(bench, []byte(`{"models":{"color-synthetic":{"latency_ms":6.1,"fps":163,"map50":98.2,"source":"measured"}}}`), 0o644))
	require.NoError(t, seed.Run(context.Background(), e.st, bench, 10*time.Minute))
	cams, err := e.st.ListCameras(context.Background())
	require.NoError(t, err)
	assert.Len(t, cams, 2)
	models, err := e.st.ListModels(context.Background())
	require.NoError(t, err)
	for _, m := range models {
		if m.ID == "color-synthetic" {
			assert.InDelta(t, 98.2, *m.MAP50, 0.01)
		}
	}
	_ = fmt.Sprintf
}

func TestSeedHistoryPopulatesAnalytics(t *testing.T) {
	st := testutil.Store(t)
	testutil.Reset(t, st)
	require.NoError(t, seed.Run(context.Background(), st, "", 30*time.Minute))
	page, err := st.ListIncidents(context.Background(), domain.IncidentFilter{Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 5, page.Total)
	counts, err := st.Counts(context.Background(), "", time.Now().Add(-time.Hour), time.Now(), "5m")
	require.NoError(t, err)
	assert.NotEmpty(t, counts)
}
