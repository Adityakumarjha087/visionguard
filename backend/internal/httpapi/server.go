// Package httpapi exposes the REST + WebSocket API with Gin.
package httpapi

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/ingest"
	"visionguard/backend/internal/store"
	"visionguard/backend/internal/ws"
)

//go:embed openapi.yaml
var openapiSpec []byte

// Server bundles handler dependencies.
type Server struct {
	Cfg    config.Config
	Store  *store.Store
	Bus    *bus.Bus
	Issuer *auth.Issuer
	Hub    *ws.Hub
	Syncer *ingest.Syncer
	// Frames, when set, serves camera streams/snapshots in-process (simulator mode)
	// instead of proxying the inference service.
	Frames FrameSource
	proxy  *httputil.ReverseProxy
}

// FrameSource provides the latest JPEG frame per camera.
type FrameSource interface {
	JPEG(cameraID string) []byte
	Interval() time.Duration
}

// New constructs the server.
func New(cfg config.Config, st *store.Store, b *bus.Bus, issuer *auth.Issuer, hub *ws.Hub, syncer *ingest.Syncer) *Server {
	target, _ := url.Parse(cfg.InferenceURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // stream MJPEG frames immediately
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_gateway","message":"inference service unavailable"}}`))
	}
	return &Server{Cfg: cfg, Store: st, Bus: b, Issuer: issuer, Hub: hub, Syncer: syncer, proxy: proxy}
}

// Router builds the Gin engine with all routes and middleware.
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.MaxMultipartMemory = 32 << 20
	_ = r.SetTrustedProxies(nil)
	r.Use(recovery(), requestID(), securityHeaders(), cors(s.Cfg.CORSOrigins), accessLog())
	r.NoRoute(func(c *gin.Context) { abort(c, errNotFound("route")) })

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", s.ready)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/api/docs", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerHTML)) })
	r.GET("/api/docs/openapi.yaml", func(c *gin.Context) { c.Data(http.StatusOK, "application/yaml", openapiSpec) })

	general := NewRateLimiter(s.Cfg.RateLimitRPS, s.Cfg.RateLimitBurst)
	authLimit := NewRateLimiter(s.Cfg.AuthRateLimit/60, int(s.Cfg.AuthRateLimit))
	v1 := r.Group("/api/v1", general.Middleware("api:"))

	pub := v1.Group("/auth")
	pub.POST("/register", authLimit.Middleware("auth:"), s.register)
	pub.POST("/login", authLimit.Middleware("auth:"), s.login)
	pub.POST("/refresh", s.refresh)
	pub.POST("/logout", s.logout)

	// Media/stream/ws accept ?access_token= because <img>/<video>/WebSocket cannot set headers.
	media := v1.Group("", authenticate(s.Issuer, true))
	media.GET("/cameras/:id/stream", s.cameraStream)
	media.GET("/cameras/:id/snapshot", s.cameraSnapshot)
	media.GET("/media/:kind/:file", s.media)
	media.GET("/ws", s.websocket)

	api := v1.Group("", authenticate(s.Issuer, false))
	op := requireRole(domain.RoleOperator)
	admin := requireRole(domain.RoleAdmin)

	api.GET("/auth/me", s.me)
	api.GET("/users", admin, s.listUsers)
	api.PATCH("/users/:id", admin, s.updateUser)
	api.DELETE("/users/:id", admin, s.deleteUser)

	api.GET("/cameras", s.listCameras)
	api.POST("/cameras", op, s.createCamera)
	api.POST("/cameras/upload", op, s.uploadCamera)
	api.GET("/cameras/:id", s.getCamera)
	api.PATCH("/cameras/:id", op, s.updateCamera)
	api.DELETE("/cameras/:id", op, s.deleteCamera)
	api.GET("/samples", s.listSamples)

	api.GET("/cameras/:id/zones", s.listZones)
	api.POST("/cameras/:id/zones", op, s.createZone)
	api.PUT("/zones/:id", op, s.updateZone)
	api.DELETE("/zones/:id", op, s.deleteZone)

	api.GET("/rules", s.listRules)
	api.POST("/cameras/:id/rules", op, s.createRule)
	api.PUT("/rules/:id", op, s.updateRule)
	api.DELETE("/rules/:id", op, s.deleteRule)

	api.GET("/incidents", s.listIncidents)
	api.GET("/incidents/:id", s.getIncident)
	api.PATCH("/incidents/:id", op, s.updateIncident)

	api.GET("/analytics/summary", s.summary)
	api.GET("/analytics/counts", s.counts)
	api.GET("/analytics/heatmap", s.heatmap)
	api.GET("/analytics/line-crossings", s.crossings)

	api.GET("/models", s.listModels)
	api.POST("/models/:id/activate", admin, s.activateModel)
	return r
}

func (s *Server) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	checks := gin.H{"database": "ok", "redis": "ok"}
	status := http.StatusOK
	if err := s.Store.Ping(ctx); err != nil {
		checks["database"], status = err.Error(), http.StatusServiceUnavailable
	}
	if err := s.Bus.Ping(ctx); err != nil {
		checks["redis"], status = err.Error(), http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK], "checks": checks})
}

// syncConfig republishes desired state after a mutation; failures are logged, not fatal,
// because the periodic resync will converge.
func (s *Server) syncConfig(c *gin.Context) {
	if s.Syncer == nil {
		return
	}
	if err := s.Syncer.Sync(c.Request.Context()); err != nil {
		c.Error(err) //nolint:errcheck
	}
}

const swaggerHTML = `<!doctype html><html><head><meta charset="utf-8"><title>VisionGuard API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css"></head>
<body><div id="swagger"></div><script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url: "/api/docs/openapi.yaml", dom_id: "#swagger"});</script></body></html>`
