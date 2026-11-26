package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"visionguard/backend/internal/domain"
	"visionguard/backend/internal/store"
)

var (
	videoExts = []string{".mp4", ".avi", ".mov", ".mkv", ".webm", ".m4v"}
	safeFile  = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(jpg|mp4)$`)
)

func (s *Server) withStatus(ctx context.Context, cams []domain.Camera) []domain.Camera {
	ids := make([]string, len(cams))
	for i, c := range cams {
		ids[i] = c.ID
	}
	statuses, err := s.Bus.Statuses(ctx, ids)
	for i := range cams {
		st, ok := statuses[cams[i].ID]
		if err != nil || !ok {
			st = domain.CameraStatus{State: "offline"}
		}
		if !cams[i].Enabled {
			st = domain.CameraStatus{State: "disabled"}
		}
		cams[i].Status = st
	}
	return cams
}

func (s *Server) listCameras(c *gin.Context) {
	cams, err := s.Store.ListCameras(c.Request.Context())
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, s.withStatus(c.Request.Context(), cams))
}

func (s *Server) getCamera(c *gin.Context) {
	cam, err := s.Store.GetCamera(c.Request.Context(), c.Param("id"))
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, s.withStatus(c.Request.Context(), []domain.Camera{cam})[0])
}

type createCameraReq struct {
	Name        string   `json:"name" binding:"required,min=1,max=100"`
	SourceType  string   `json:"source_type" binding:"required,oneof=rtsp webcam file"`
	SourceURI   string   `json:"source_uri" binding:"required,max=2048"`
	Enabled     *bool    `json:"enabled"`
	PrivacyMode *bool    `json:"privacy_mode"`
	TargetFPS   *float64 `json:"target_fps" binding:"omitempty,gt=0,lte=30"`
}

// validateSource checks the URI against its source type; file sources must exist under the video dir.
func (s *Server) validateSource(sourceType, uri string) *APIError {
	switch sourceType {
	case "rtsp":
		u, err := url.Parse(uri)
		if err != nil || !slices.Contains([]string{"rtsp", "rtsps", "http", "https"}, u.Scheme) || u.Host == "" {
			return errValidation("invalid source", map[string]string{"source_uri": "must be an rtsp://, rtsps:// or http(s):// URL"})
		}
	case "webcam":
		if len(uri) > 64 {
			return errValidation("invalid source", map[string]string{"source_uri": "must be a device index or path"})
		}
	case "file":
		clean := filepath.Clean(uri)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return errValidation("invalid source", map[string]string{"source_uri": "must be relative to the video directory"})
		}
		if _, err := os.Stat(filepath.Join(s.Cfg.DataDir, "videos", clean)); err != nil {
			return errValidation("invalid source", map[string]string{"source_uri": "video file not found"})
		}
	}
	return nil
}

func (s *Server) createCamera(c *gin.Context) {
	var req createCameraReq
	if !bind(c, &req) {
		return
	}
	if apiErr := s.validateSource(req.SourceType, req.SourceURI); apiErr != nil {
		abort(c, apiErr)
		return
	}
	in := store.CameraInput{Name: strings.TrimSpace(req.Name), SourceType: req.SourceType, SourceURI: req.SourceURI,
		Enabled: deref(req.Enabled, true), PrivacyMode: deref(req.PrivacyMode, true), TargetFPS: deref(req.TargetFPS, 10)}
	cam, err := s.Store.CreateCamera(c.Request.Context(), in)
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusCreated, s.withStatus(c.Request.Context(), []domain.Camera{cam})[0])
}

type patchCameraReq struct {
	Name        *string  `json:"name" binding:"omitempty,min=1,max=100"`
	SourceURI   *string  `json:"source_uri" binding:"omitempty,max=2048"`
	Enabled     *bool    `json:"enabled"`
	PrivacyMode *bool    `json:"privacy_mode"`
	TargetFPS   *float64 `json:"target_fps" binding:"omitempty,gt=0,lte=30"`
}

func (s *Server) updateCamera(c *gin.Context) {
	var req patchCameraReq
	if !bind(c, &req) {
		return
	}
	ctx := c.Request.Context()
	if req.SourceURI != nil {
		cam, err := s.Store.GetCamera(ctx, c.Param("id"))
		if err != nil {
			abort(c, err)
			return
		}
		if apiErr := s.validateSource(cam.SourceType, *req.SourceURI); apiErr != nil {
			abort(c, apiErr)
			return
		}
	}
	cam, err := s.Store.UpdateCamera(ctx, c.Param("id"), store.CameraPatch{
		Name: req.Name, SourceURI: req.SourceURI, Enabled: req.Enabled, PrivacyMode: req.PrivacyMode, TargetFPS: req.TargetFPS})
	if err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusOK, s.withStatus(ctx, []domain.Camera{cam})[0])
}

func (s *Server) deleteCamera(c *gin.Context) {
	if err := s.Store.DeleteCamera(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.Status(http.StatusNoContent)
}

// uploadCamera stores an uploaded video and registers it as a looping file camera.
func (s *Server) uploadCamera(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.Cfg.MaxUploadBytes+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			abort(c, &APIError{Status: http.StatusRequestEntityTooLarge, Code: "bad_request", Message: "file too large"})
			return
		}
		abort(c, errValidation("file is required", map[string]string{"file": "is required"}))
		return
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !slices.Contains(videoExts, ext) {
		abort(c, errValidation("unsupported file type", map[string]string{"file": "must be one of " + strings.Join(videoExts, ", ")}))
		return
	}
	if fh.Size > s.Cfg.MaxUploadBytes {
		abort(c, &APIError{Status: http.StatusRequestEntityTooLarge, Code: "bad_request", Message: "file too large"})
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(fh.Filename), ext)
	}
	if len(name) > 100 {
		name = name[:100]
	}
	rel := filepath.Join("uploads", uuid.NewString()+ext)
	dst := filepath.Join(s.Cfg.DataDir, "videos", rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		abort(c, err)
		return
	}
	if err := saveUpload(fh.Open, dst); err != nil {
		abort(c, err)
		return
	}
	cam, err := s.Store.CreateCamera(c.Request.Context(), store.CameraInput{
		Name: name, SourceType: "file", SourceURI: filepath.ToSlash(rel), Enabled: true, PrivacyMode: true, TargetFPS: 10})
	if err != nil {
		_ = os.Remove(dst)
		abort(c, err)
		return
	}
	s.syncConfig(c)
	c.JSON(http.StatusCreated, s.withStatus(c.Request.Context(), []domain.Camera{cam})[0])
}

func saveUpload[F io.ReadCloser](open func() (F, error), dst string) error {
	src, err := open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

type sample struct {
	Name      string `json:"name"`
	SourceURI string `json:"source_uri"`
	SizeBytes int64  `json:"size_bytes"`
}

func (s *Server) listSamples(c *gin.Context) {
	dir := filepath.Join(s.Cfg.DataDir, "videos", "samples")
	entries, err := os.ReadDir(dir)
	out := []sample{}
	if err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if e.IsDir() || !slices.Contains(videoExts, ext) || strings.Contains(e.Name(), ".tmp.") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, sample{Name: strings.TrimSuffix(e.Name(), ext), SourceURI: "samples/" + e.Name(), SizeBytes: info.Size()})
		}
	}
	c.JSON(http.StatusOK, out)
}

// cameraStream proxies the inference MJPEG stream (auth enforced here, not in the inference service).
func (s *Server) cameraStream(c *gin.Context) { s.proxyInference(c, "/stream/") }

func (s *Server) cameraSnapshot(c *gin.Context) { s.proxyInference(c, "/snapshot/") }

func (s *Server) proxyInference(c *gin.Context, prefix string) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		abort(c, errNotFound("camera"))
		return
	}
	if _, err := s.Store.GetCamera(c.Request.Context(), id); err != nil {
		abort(c, err)
		return
	}
	req := c.Request.Clone(c.Request.Context())
	req.URL.Path = prefix + id
	req.URL.RawQuery = ""
	req.Header.Del("Authorization")
	s.proxy.ServeHTTP(c.Writer, req)
}

// media serves snapshots/clips from the shared volume (Range requests supported for video).
func (s *Server) media(c *gin.Context) {
	kind, file := c.Param("kind"), c.Param("file")
	if (kind != "snapshots" && kind != "clips") || !safeFile.MatchString(file) {
		abort(c, errNotFound("media"))
		return
	}
	path := filepath.Join(s.Cfg.DataDir, "media", kind, file)
	if _, err := os.Stat(path); err != nil {
		abort(c, errNotFound("media"))
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

