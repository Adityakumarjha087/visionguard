package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/domain"
)

type authResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    int         `json:"expires_in"`
	User         domain.User `json:"user"`
}

func (s *Server) issueTokens(ctx context.Context, u domain.User) (authResponse, error) {
	access, err := s.Issuer.AccessToken(u)
	if err != nil {
		return authResponse{}, err
	}
	refresh, hash, err := auth.NewRefreshToken()
	if err != nil {
		return authResponse{}, err
	}
	if err := s.Store.StoreRefreshToken(ctx, u.ID, hash, time.Now().Add(s.Issuer.RefreshTTL())); err != nil {
		return authResponse{}, err
	}
	return authResponse{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer",
		ExpiresIn: int(s.Issuer.AccessTTL().Seconds()), User: u}, nil
}

type registerReq struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=8,max=128"`
	Name     string `json:"name" binding:"required,min=1,max=100"`
}

func (s *Server) register(c *gin.Context) {
	var req registerReq
	if !bind(c, &req) {
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		abort(c, err)
		return
	}
	u, err := s.Store.CreateUser(c.Request.Context(), strings.TrimSpace(req.Email), strings.TrimSpace(req.Name), hash, domain.RoleViewer)
	if errors.Is(err, domain.ErrConflict) {
		abort(c, errConflict("an account with this email already exists"))
		return
	}
	if err != nil {
		abort(c, err)
		return
	}
	resp, err := s.issueTokens(c.Request.Context(), u)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

type loginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (s *Server) login(c *gin.Context) {
	var req loginReq
	if !bind(c, &req) {
		return
	}
	u, err := s.Store.UserByEmail(c.Request.Context(), strings.TrimSpace(req.Email))
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		abort(c, err)
		return
	}
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		abort(c, errUnauthorized("invalid email or password"))
		return
	}
	resp, err := s.issueTokens(c.Request.Context(), u)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// refresh rotates the refresh token. Presenting an already-rotated token is treated as
// theft and revokes every session of that user.
func (s *Server) refresh(c *gin.Context) {
	var req refreshReq
	if !bind(c, &req) {
		return
	}
	ctx := c.Request.Context()
	info, err := s.Store.RefreshToken(ctx, auth.HashToken(req.RefreshToken))
	if err != nil {
		abort(c, errUnauthorized("invalid refresh token"))
		return
	}
	if info.Revoked {
		slog.Warn("refresh token reuse detected; revoking all sessions", "user_id", info.UserID)
		_ = s.Store.RevokeAllRefreshTokens(ctx, info.UserID)
		abort(c, errUnauthorized("refresh token already used"))
		return
	}
	if time.Now().After(info.ExpiresAt) {
		abort(c, errUnauthorized("refresh token expired"))
		return
	}
	if ok, err := s.Store.RevokeRefreshToken(ctx, info.ID); err != nil || !ok {
		abort(c, errUnauthorized("refresh token already used"))
		return
	}
	u, err := s.Store.UserByID(ctx, info.UserID)
	if err != nil {
		abort(c, errUnauthorized("user no longer exists"))
		return
	}
	resp, err := s.issueTokens(ctx, u)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) logout(c *gin.Context) {
	var req refreshReq
	if !bind(c, &req) {
		return
	}
	if info, err := s.Store.RefreshToken(c.Request.Context(), auth.HashToken(req.RefreshToken)); err == nil {
		_, _ = s.Store.RevokeRefreshToken(c.Request.Context(), info.ID)
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) me(c *gin.Context) {
	u, err := s.Store.UserByID(c.Request.Context(), currentClaims(c).Subject)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (s *Server) listUsers(c *gin.Context) {
	users, err := s.Store.ListUsers(c.Request.Context())
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, users)
}

type updateUserReq struct {
	Name *string      `json:"name" binding:"omitempty,min=1,max=100"`
	Role *domain.Role `json:"role" binding:"omitempty,oneof=admin operator viewer"`
}

func (s *Server) updateUser(c *gin.Context) {
	var req updateUserReq
	if !bind(c, &req) {
		return
	}
	ctx := c.Request.Context()
	if req.Role != nil && *req.Role != domain.RoleAdmin && c.Param("id") == currentClaims(c).Subject {
		abort(c, errConflict("you cannot remove your own admin role"))
		return
	}
	u, err := s.Store.UpdateUser(ctx, c.Param("id"), req.Name, req.Role)
	if err != nil {
		abort(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (s *Server) deleteUser(c *gin.Context) {
	if c.Param("id") == currentClaims(c).Subject {
		abort(c, errConflict("you cannot delete your own account"))
		return
	}
	if err := s.Store.DeleteUser(c.Request.Context(), c.Param("id")); err != nil {
		abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
