package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/ingest"
	"visionguard/backend/internal/store"
)

// ---------------------------------------------------------------- incidents

func parseTime(c *gin.Context, key string) (*time.Time, *APIError) {
	raw := c.Query(key)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errValidation("invalid query", map[string]string{key: "must be an RFC 3339 timestamp"})
	}
	return &t, nil
}

func queryInt(c *gin.Context, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

func oneOf(c *gin.Context, key string, allowed ...string) (string, *APIError) {
	v := c.Query(key)
	if v != "" && !slices.Contains(allowed, v) {
		return "", errValidation("invalid query", map[string]string{key: "invalid value"})
	}
	return v, nil
}

func (s *Server) listIncidents(c *gin.Context) {
	f := domain.IncidentFilter{Query: c.Query("q"), CameraID: c.Query("camera_id"),
		Page: queryInt(c, "page", 1, 1, 100000), PageSize: queryInt(c, "page_size", 20, 1, 100)}
	var apiErr *APIError
	if f.Severity, apiErr = oneOf(c, "severity", "low", "medium", "high", "critical"); apiErr != nil {
		abort(c, apiErr)
		return
	}
	if f.Status, apiErr = oneOf(c, "status", "open", "acknowledged", "resolved"); apiErr != nil {
		abort(c, apiErr)
		return
	}
	if f.RuleType, apiErr = oneOf(c, "rule_type", "zone_intrusion", "dwell_time", "missing_ppe", "crowd_count", "line_crossing"); apiErr != nil {
		abort(c, apiErr)
		return
	}
	if f.From, apiErr = parseTime(c, "from"); apiErr != nil {
		abort(c, apiErr)
		return
	}
	if f.To, apiErr = parseTime(c, "to"); apiErr != nil {
		abort(c, apiErr)
		return
	}
	page, err := s.Store.ListIncidents(c.Request.Context(), f)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (s *Server) getIncident(c *gin.Context) {
	inc, err := s.Store.GetIncident(c.Request.Context(), c.Param("id"))
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, inc)
}

type incidentPatch struct {
	Status string `json:"status" binding:"required,oneof=open acknowledged resolved"`
}

func (s *Server) updateIncident(c *gin.Context) {
	var req incidentPatch
	if !bind(c, &req) {
		return
	}
	inc, err := s.Store.SetIncidentStatus(c.Request.Context(), c.Param("id"), req.Status, currentClaims(c).Subject)
	if err != nil {
		abort(c, err)
		return
	}
	s.Hub.Broadcast("incident_update", inc)
	c.JSON(http.StatusOK, inc)
}

// ---------------------------------------------------------------- analytics

func timeRange(c *gin.Context) (time.Time, time.Time, *APIError) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	f, apiErr := parseTime(c, "from")
	if apiErr != nil {
		return from, to, apiErr
	}
	t, apiErr := parseTime(c, "to")
	if apiErr != nil {
		return from, to, apiErr
	}
	if t != nil {
		to = *t
	}
	if f != nil {
		from = *f
	} else {
		from = to.Add(-time.Hour)
	}
	if !from.Before(to) {
		return from, to, errValidation("invalid range", map[string]string{"from": "must be before to"})
	}
	if to.Sub(from) > 31*24*time.Hour {
		return from, to, errValidation("invalid range", map[string]string{"from": "range must be at most 31 days"})
	}
	return from, to, nil
}

func bucket(c *gin.Context, def string) (string, *APIError) {
	b := c.DefaultQuery("bucket", def)
	if _, ok := store.Buckets[b]; !ok {
		return "", errValidation("invalid bucket", map[string]string{"bucket": "must be one of 10s, 1m, 5m, 15m, 1h"})
	}
	return b, nil
}

func (s *Server) summary(c *gin.Context) {
	ctx := c.Request.Context()
	sum, err := s.Store.SummaryCounts(ctx, c.Query("camera_id"))
	if err != nil {
		abort(c, err)
		return
	}
	cams, err := s.Store.ListCameras(ctx)
	if err == nil {
		for _, cam := range s.withStatus(ctx, cams) {
			if cam.Status.State == "online" && (c.Query("camera_id") == "" || cam.ID == c.Query("camera_id")) {
				sum.CamerasOnline++
			}
		}
	}
	c.JSON(http.StatusOK, sum)
}

func (s *Server) counts(c *gin.Context) {
	from, to, apiErr := timeRange(c)
	if apiErr != nil {
		abort(c, apiErr)
		return
	}
	b, apiErr := bucket(c, "1m")
	if apiErr != nil {
		abort(c, apiErr)
		return
	}
	out, err := s.Store.Counts(c.Request.Context(), c.Query("camera_id"), from, to, b)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) heatmap(c *gin.Context) {
	camID := c.Query("camera_id")
	if camID == "" {
		abort(c, errValidation("camera_id is required", map[string]string{"camera_id": "is required"}))
		return
	}
	if _, err := s.Store.GetCamera(c.Request.Context(), camID); err != nil {
		abort(c, errNotFound("camera"))
		return
	}
	from, to, apiErr := timeRange(c)
	if apiErr != nil {
		abort(c, apiErr)
		return
	}
	hm, err := s.Store.Heatmap(c.Request.Context(), camID, from, to, queryInt(c, "grid", 32, 8, 128))
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, hm)
}

func (s *Server) crossings(c *gin.Context) {
	from, to, apiErr := timeRange(c)
	if apiErr != nil {
		abort(c, apiErr)
		return
	}
	b, apiErr := bucket(c, "5m")
	if apiErr != nil {
		abort(c, apiErr)
		return
	}
	out, err := s.Store.Crossings(c.Request.Context(), c.Query("camera_id"), from, to, b)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ---------------------------------------------------------------- models

func (s *Server) models(c *gin.Context) ([]domain.Model, error) {
	ctx := c.Request.Context()
	// Refresh availability from the live catalogue when the inference service is up.
	if live, err := s.Bus.Models(ctx); err == nil {
		for _, m := range live {
			if err := s.Store.UpsertModel(ctx, m); err != nil {
				return nil, err
			}
		}
	}
	models, err := s.Store.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	active := s.Syncer.ActiveModel(ctx)
	for i := range models {
		models[i].Active = models[i].ID == active
		if models[i].Classes == nil {
			models[i].Classes = []string{}
		}
	}
	return models, nil
}

func (s *Server) listModels(c *gin.Context) {
	models, err := s.models(c)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, models)
}

func (s *Server) activateModel(c *gin.Context) {
	models, err := s.models(c)
	if err != nil {
		abort(c, err)
		return
	}
	idx := slices.IndexFunc(models, func(m domain.Model) bool { return m.ID == c.Param("id") })
	if idx < 0 {
		abort(c, errNotFound("model"))
		return
	}
	if !models[idx].Available {
		abort(c, errConflict("model weights are not available on the inference host"))
		return
	}
	if err := s.Store.SetSetting(c.Request.Context(), ingest.ActiveModelKey, models[idx].ID); err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	m := models[idx]
	m.Active = true
	c.JSON(http.StatusOK, m)
}

// ---------------------------------------------------------------- websocket

func (s *Server) websocket(c *gin.Context) {
	claims := currentClaims(c)
	if claims == nil {
		abort(c, errors.New("missing claims"))
		return
	}
	s.Hub.Serve(c.Writer, c.Request, claims.Subject)
}
