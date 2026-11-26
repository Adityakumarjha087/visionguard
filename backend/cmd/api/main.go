// Command api runs the VisionGuard API: `api serve` (default), `api migrate`, `api seed`.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"visionguard/backend/internal/auth"
	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/config"
	"visionguard/backend/internal/db"
	"visionguard/backend/internal/httpapi"
	"visionguard/backend/internal/ingest"
	"visionguard/backend/internal/notify"
	"visionguard/backend/internal/seed"
	"visionguard/backend/internal/store"
	"visionguard/backend/internal/ws"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	setupLogging(cfg.LogLevel)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cmd, cfg); err != nil {
		slog.Error("fatal", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func setupLogging(level string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).With("service", "api"))
}

func run(ctx context.Context, cmd string, cfg config.Config) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	st := store.New(pool)
	benchmarks := os.Getenv("VG_BENCHMARKS_FILE")
	switch cmd {
	case "migrate":
		return nil
	case "seed":
		return seed.Run(ctx, st, benchmarks, 6*time.Hour)
	case "serve":
	default:
		return fmt.Errorf("unknown command %q (serve|migrate|seed)", cmd)
	}
	if os.Getenv("VG_AUTO_SEED") == "true" {
		if err := seed.Run(ctx, st, benchmarks, 6*time.Hour); err != nil {
			return fmt.Errorf("auto seed: %w", err)
		}
	}

	b, err := bus.New(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = b.R.Close() }()
	issuer := auth.NewIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTTL, cfg.RefreshTTL)
	hub := ws.NewHub(cfg.CORSOrigins)
	syncer := &ingest.Syncer{Store: st, Bus: b, DefaultModel: cfg.DefaultModel}
	proc := &ingest.Processor{Store: st, Hub: hub, Notifier: notify.NewDispatcher(cfg), Syncer: syncer}

	go syncer.Run(ctx, cfg.ConfigResync)
	go b.ConsumeEvents(ctx, cfg.ConsumerName, proc.Handle)
	go b.TailFrames(ctx, hub.PublishFrame)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           httpapi.New(cfg, st, b, issuer, hub, syncer).Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", srv.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
