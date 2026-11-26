package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/domain"
)

const (
	requestIDKey = "request_id"
	claimsKey    = "claims"
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vg_http_requests_total", Help: "HTTP requests"}, []string{"method", "route", "status"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "vg_http_request_duration_seconds", Help: "HTTP request latency",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5}}, []string{"method", "route"})
)

func init() { prometheus.MustRegister(httpRequests, httpDuration) }

// requestID propagates/creates X-Request-ID.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// accessLog emits one structured log line per request plus Prometheus metrics.
func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		elapsed := time.Since(start)
		httpRequests.WithLabelValues(c.Request.Method, route, strconv.Itoa(status)).Inc()
		if !strings.HasSuffix(route, "/stream") && !strings.HasSuffix(route, "/ws") {
			httpDuration.WithLabelValues(c.Request.Method, route).Observe(elapsed.Seconds())
		}
		if route == "/metrics" || route == "/healthz" {
			return
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(c.Request.Context(), level, "http request", "method", c.Request.Method, "route", route,
			"status", status, "duration_ms", elapsed.Milliseconds(), "ip", c.ClientIP(),
			"request_id", c.GetString(requestIDKey))
	}
}

// recovery converts panics into 500 JSON errors.
func recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, rec any) {
		slog.Error("panic recovered", "panic", rec, "path", c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusInternalServerError,
			gin.H{"error": APIError{Code: "internal", Message: "internal server error"}})
	})
}

// cors allows configured browser origins.
func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowed[origin] || allowed["*"]) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
			h.Set("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// securityHeaders sets conservative defaults for an API.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// RateLimiter is a per-key token bucket with idle eviction.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*limiterEntry
	limit   rate.Limit
	burst   int
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter creates a limiter allowing rps with burst.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{buckets: map[string]*limiterEntry{}, limit: rate.Limit(rps), burst: burst}
}

// Allow reports whether key may proceed.
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	e, ok := r.buckets[key]
	if !ok {
		if len(r.buckets) > 10000 {
			for k, v := range r.buckets {
				if now.Sub(v.seen) > 10*time.Minute {
					delete(r.buckets, k)
				}
			}
		}
		e = &limiterEntry{lim: rate.NewLimiter(r.limit, r.burst)}
		r.buckets[key] = e
	}
	e.seen = now
	return e.lim.Allow()
}

// Middleware rejects over-limit clients with 429.
func (r *RateLimiter) Middleware(prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !r.Allow(prefix + c.ClientIP()) {
			c.Header("Retry-After", "1")
			abort(c, &APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many requests"})
			return
		}
		c.Next()
	}
}

// authenticate validates the bearer token (or ?access_token= when allowQuery).
func authenticate(issuer *auth.Issuer, allowQuery bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ""
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		} else if allowQuery && c.Request.Method == http.MethodGet {
			token = c.Query("access_token")
		}
		if token == "" {
			abort(c, errUnauthorized("missing bearer token"))
			return
		}
		claims, err := issuer.Parse(token)
		if err != nil {
			abort(c, errUnauthorized("invalid or expired token"))
			return
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

// requireRole enforces RBAC (role hierarchy admin > operator > viewer).
func requireRole(min domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims := currentClaims(c); claims == nil || !claims.Role.AtLeast(min) {
			abort(c, errForbidden())
			return
		}
		c.Next()
	}
}

func currentClaims(c *gin.Context) *auth.Claims {
	v, ok := c.Get(claimsKey)
	if !ok {
		return nil
	}
	claims, _ := v.(*auth.Claims)
	return claims
}
