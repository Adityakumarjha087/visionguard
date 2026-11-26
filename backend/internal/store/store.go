// Package store implements persistence on PostgreSQL/TimescaleDB with pgx.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"visionguard/backend/internal/domain"
)

// Store is the single entry point to the database.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps a pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the pool (health checks, tests).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return domain.ErrConflict
		case "23503", "22P02": // FK violation, invalid uuid text
			return domain.ErrNotFound
		}
	}
	return err
}

// ---------------------------------------------------------------- users

const userCols = `id, email, name, role, password_hash, created_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.CreatedAt)
	return u, mapErr(err)
}

func (s *Store) CreateUser(ctx context.Context, email, name, hash string, role domain.Role) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash, role) VALUES (lower($1), $2, $3, $4) RETURNING `+userCols,
		email, name, hash, role))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = lower($1)`, email))
}

func (s *Store) UserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id string, name *string, role *domain.Role) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`UPDATE users SET name = coalesce($2, name), role = coalesce($3, role) WHERE id = $1 RETURNING `+userCols,
		id, name, role))
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------- refresh tokens

func (s *Store) StoreRefreshToken(ctx context.Context, userID, hash string, expires time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`, userID, hash, expires)
	return mapErr(err)
}

// RefreshTokenInfo describes a stored refresh token.
type RefreshTokenInfo struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	Revoked   bool
}

func (s *Store) RefreshToken(ctx context.Context, hash string) (RefreshTokenInfo, error) {
	var t RefreshTokenInfo
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, expires_at, revoked_at FROM refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.ExpiresAt, &revokedAt)
	t.Revoked = revokedAt != nil
	return t, mapErr(err)
}

// RevokeRefreshToken revokes one token; returns false if it was already revoked (reuse).
func (s *Store) RevokeRefreshToken(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) RevokeAllRefreshTokens(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// ---------------------------------------------------------------- settings

// GetSetting JSON-decodes a setting into dest.
func (s *Store) GetSetting(ctx context.Context, key string, dest any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value::text FROM settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return mapErr(err)
	}
	return json.Unmarshal(raw, dest)
}

// SetSetting JSON-encodes and upserts a setting.
func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, key, string(raw))
	return err
}
