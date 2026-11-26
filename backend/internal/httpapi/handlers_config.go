package httpapi

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/store"
)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type zoneReq struct {
	Name   string         `json:"name" binding:"required,min=1,max=100"`
	Kind   string         `json:"kind" binding:"required,oneof=polygon line"`
	Points []domain.Point `json:"points" binding:"required,max=64"`
	Color  string         `json:"color"`
}

func (r *zoneReq) validate() *APIError {
	details := map[string]string{}
	switch r.Kind {
	case "polygon":
		if len(r.Points) < 3 {
			details["points"] = "a polygon needs at least 3 points"
		}
	case "line":
		if len(r.Points) != 2 {
			details["points"] = "a line needs exactly 2 points"
		}
	}
	for _, p := range r.Points {
		if p[0] < 0 || p[0] > 1 || p[1] < 0 || p[1] > 1 {
			details["points"] = "coordinates must be normalised to [0, 1]"
			break
		}
	}
	if r.Color == "" {
		r.Color = "#22c55e"
	} else if !hexColor.MatchString(r.Color) {
		details["color"] = "must be a #rrggbb hex colour"
	}
	if len(details) > 0 {
		return errValidation("invalid zone", details)
	}
	return nil
}

func (r zoneReq) input() store.ZoneInput {
	return store.ZoneInput{Name: strings.TrimSpace(r.Name), Kind: r.Kind, Points: r.Points, Color: r.Color}
}

func (s *Server) listZones(c *gin.Context) {
	if _, err := s.Store.GetCamera(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, err)
		return
	}
	zones, err := s.Store.ListZones(c.Request.Context(), c.Param("id"))
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, zones)
}

func (s *Server) createZone(c *gin.Context) {
	var req zoneReq
	if !bind(c, &req) {
		return
	}
	if apiErr := req.validate(); apiErr != nil {
		abort(c, apiErr)
		return
	}
	if _, err := s.Store.GetCamera(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, errNotFound("camera"))
		return
	}
	z, err := s.Store.CreateZone(c.Request.Context(), c.Param("id"), req.input())
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusCreated, z)
}

func (s *Server) updateZone(c *gin.Context) {
	var req zoneReq
	if !bind(c, &req) {
		return
	}
	if apiErr := req.validate(); apiErr != nil {
		abort(c, apiErr)
		return
	}
	z, err := s.Store.UpdateZone(c.Request.Context(), c.Param("id"), req.input())
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusOK, z)
}

func (s *Server) deleteZone(c *gin.Context) {
	if err := s.Store.DeleteZone(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.Status(http.StatusNoContent)
}

// ---------------------------------------------------------------- rules

type ruleReq struct {
	Name           string            `json:"name" binding:"required,min=1,max=100"`
	Type           string            `json:"type" binding:"required,oneof=zone_intrusion dwell_time missing_ppe crowd_count line_crossing"`
	ZoneID         *string           `json:"zone_id"`
	Params         domain.RuleParams `json:"params"`
	Severity       string            `json:"severity" binding:"omitempty,oneof=low medium high critical"`
	Enabled        *bool             `json:"enabled"`
	NotifyEmail    bool              `json:"notify_email"`
	NotifyTelegram bool              `json:"notify_telegram"`
}

// validate checks type-specific zone requirements and parameter ranges.
func (s *Server) validateRule(c *gin.Context, cameraID string, r *ruleReq) *APIError {
	details := map[string]string{}
	if r.ZoneID != nil && *r.ZoneID == "" {
		r.ZoneID = nil
	}
	needs := map[string]string{"zone_intrusion": "polygon", "dwell_time": "polygon", "line_crossing": "line"}
	want, required := needs[r.Type]
	if r.ZoneID == nil && required {
		details["zone_id"] = "is required for " + r.Type
	}
	if r.ZoneID != nil {
		z, err := s.Store.GetZone(c.Request.Context(), *r.ZoneID)
		switch {
		case err != nil || z.CameraID != cameraID:
			details["zone_id"] = "zone not found on this camera"
		case required && z.Kind != want:
			details["zone_id"] = r.Type + " requires a " + want + " zone"
		case !required && z.Kind != "polygon":
			details["zone_id"] = r.Type + " requires a polygon zone"
		}
	}
	p := r.Params
	if p.Seconds != nil && (*p.Seconds < 0 || *p.Seconds > 86400) {
		details["params.seconds"] = "must be between 0 and 86400"
	}
	if p.MaxCount != nil && (*p.MaxCount < 0 || *p.MaxCount > 1000) {
		details["params.max_count"] = "must be between 0 and 1000"
	}
	if p.CooldownSeconds != nil && (*p.CooldownSeconds < 0 || *p.CooldownSeconds > 86400) {
		details["params.cooldown_seconds"] = "must be between 0 and 86400"
	}
	if len(p.Classes) > 20 {
		details["params.classes"] = "at most 20 classes"
	}
	if r.Severity == "" {
		r.Severity = "medium"
	}
	if len(details) > 0 {
		return errValidation("invalid rule", details)
	}
	return nil
}

func (r ruleReq) input() store.RuleInput {
	return store.RuleInput{ZoneID: r.ZoneID, Name: strings.TrimSpace(r.Name), Type: r.Type, Params: r.Params,
		Severity: r.Severity, Enabled: deref(r.Enabled, true), NotifyEmail: r.NotifyEmail, NotifyTelegram: r.NotifyTelegram}
}

func (s *Server) listRules(c *gin.Context) {
	rules, err := s.Store.ListRules(c.Request.Context(), c.Query("camera_id"))
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, rules)
}

func (s *Server) createRule(c *gin.Context) {
	var req ruleReq
	if !bind(c, &req) {
		return
	}
	camID := c.Param("id")
	if _, err := s.Store.GetCamera(c.Request.Context(), camID); err != nil {
		abort(c, errNotFound("camera"))
		return
	}
	if apiErr := s.validateRule(c, camID, &req); apiErr != nil {
		abort(c, apiErr)
		return
	}
	rule, err := s.Store.CreateRule(c.Request.Context(), camID, req.input())
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusCreated, rule)
}

func (s *Server) updateRule(c *gin.Context) {
	var req ruleReq
	if !bind(c, &req) {
		return
	}
	existing, err := s.Store.GetRule(c.Request.Context(), c.Param("id"))
	if err != nil {
		abort(c, err)
		return
	}
	if apiErr := s.validateRule(c, existing.CameraID, &req); apiErr != nil {
		abort(c, apiErr)
		return
	}
	rule, err := s.Store.UpdateRule(c.Request.Context(), existing.ID, req.input())
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusOK, rule)
}

func (s *Server) deleteRule(c *gin.Context) {
	if err := s.Store.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.Status(http.StatusNoContent)
}
