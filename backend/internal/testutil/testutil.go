// Package testutil provisions a throwaway TimescaleDB database and in-memory Redis for tests.
package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/db"
	"visionguard/backend/internal/store"
)

// DefaultURL points at the compose TimescaleDB on the host.
const DefaultURL = "postgres://visionguard:visionguard@localhost:20910/visionguard?sslmode=disable" //nolint:gosec // local test default

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// Store returns a store on a freshly migrated database shared by the test binary.
// Tests are skipped when no database is reachable (set VG_TEST_DATABASE_URL in CI).
func Store(t *testing.T) *store.Store {
	t.Helper()
	once.Do(func() { pool, initErr = setup(context.Background()) })
	if initErr != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("test database unavailable: %v", initErr)
		}
		t.Skipf("test database unavailable: %v", initErr)
	}
	return store.New(pool)
}

func setup(ctx context.Context) (*pgxpool.Pool, error) {
	base := os.Getenv("VG_TEST_DATABASE_URL")
	if base == "" {
		base = DefaultURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("visionguard_test_%d", os.Getpid())
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		return nil, err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return nil, err
	}
	u.Path = "/" + name
	p, err := db.Connect(ctx, u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Reset truncates all tables between tests.
func Reset(t *testing.T, st *store.Store) {
	t.Helper()
	_, err := st.Pool().Exec(context.Background(), `TRUNCATE users, refresh_tokens, cameras, zones, rules, incidents,
		detection_counts, track_points, line_crossings, models, settings CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

// Redis starts an in-memory Redis and returns a Bus on it.
func Redis(t *testing.T) (*bus.Bus, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return &bus.Bus{R: redis.NewClient(&redis.Options{Addr: mr.Addr()})}, mr
}

// Cleanup drops test databases from previous runs (best effort).
func Cleanup(ctx context.Context) {
	if pool == nil {
		return
	}
	name := pool.Config().ConnConfig.Database
	pool.Close()
	base := os.Getenv("VG_TEST_DATABASE_URL")
	if base == "" {
		base = DefaultURL
	}
	if admin, err := pgx.Connect(ctx, base); err == nil {
		if strings.HasPrefix(name, "visionguard_test_") {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		}
		_ = admin.Close(ctx)
	}
}
