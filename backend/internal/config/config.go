// Package config loads API configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full runtime configuration of the API service.
type Config struct {
	Env             string
	Port            int
	DatabaseURL     string
	RedisURL        string
	InferenceURL    string
	DataDir         string
	JWTSecret       string
	JWTIssuer       string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	CORSOrigins     []string
	RateLimitRPS    float64
	RateLimitBurst  int
	AuthRateLimit   float64 // requests per minute on /auth/login|register
	MaxUploadBytes  int64
	LogLevel        string
	PublicURL       string
	SMTP            SMTPConfig
	Telegram        TelegramConfig
	ConfigResync    time.Duration
	ConsumerName    string
	DefaultModel    string
}

// SMTPConfig configures optional email notifications (disabled when Host is empty).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
}

// TelegramConfig configures optional Telegram notifications (disabled when BotToken is empty).
type TelegramConfig struct {
	BotToken string
	ChatID   string
	BaseURL  string
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	cfg := Config{
		Env:            env("VG_ENV", "development"),
		Port:           envInt("VG_PORT", 8080),
		DatabaseURL:    env("VG_DATABASE_URL", "postgres://visionguard:visionguard@localhost:20910/visionguard?sslmode=disable"),
		RedisURL:       env("VG_REDIS_URL", "redis://localhost:20911/0"),
		InferenceURL:   strings.TrimRight(env("VG_INFERENCE_URL", "http://localhost:20902"), "/"),
		DataDir:        env("VG_DATA_DIR", "./data"),
		JWTSecret:      env("VG_JWT_SECRET", ""),
		JWTIssuer:      env("VG_JWT_ISSUER", "visionguard"),
		AccessTTL:      envDuration("VG_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:     envDuration("VG_REFRESH_TTL", 7*24*time.Hour),
		CORSOrigins:    envList("VG_CORS_ORIGINS", []string{"http://localhost:20900", "http://127.0.0.1:20900"}),
		RateLimitRPS:   envFloat("VG_RATE_LIMIT_RPS", 30),
		RateLimitBurst: envInt("VG_RATE_LIMIT_BURST", 90),
		AuthRateLimit:  envFloat("VG_AUTH_RATE_LIMIT_PER_MIN", 20),
		MaxUploadBytes: int64(envInt("VG_MAX_UPLOAD_MB", 200)) << 20,
		LogLevel:       env("VG_LOG_LEVEL", "info"),
		PublicURL:      env("VG_PUBLIC_URL", "http://localhost:20900"),
		ConfigResync:   envDuration("VG_CONFIG_RESYNC", 30*time.Second),
		ConsumerName:   env("VG_CONSUMER_NAME", hostname()),
		DefaultModel:   env("VG_DEFAULT_MODEL", "color-synthetic"),
		SMTP: SMTPConfig{
			Host:     env("VG_SMTP_HOST", ""),
			Port:     envInt("VG_SMTP_PORT", 587),
			Username: env("VG_SMTP_USERNAME", ""),
			Password: env("VG_SMTP_PASSWORD", ""),
			From:     env("VG_SMTP_FROM", "alerts@visionguard.local"),
			To:       envList("VG_SMTP_TO", nil),
		},
		Telegram: TelegramConfig{
			BotToken: env("VG_TELEGRAM_BOT_TOKEN", ""),
			ChatID:   env("VG_TELEGRAM_CHAT_ID", ""),
			BaseURL:  env("VG_TELEGRAM_API_URL", "https://api.telegram.org"),
		},
	}
	if cfg.JWTSecret == "" {
		if cfg.Env == "production" {
			return cfg, fmt.Errorf("VG_JWT_SECRET is required in production")
		}
		cfg.JWTSecret = "dev-only-insecure-secret-change-me"
	}
	if len(cfg.JWTSecret) < 16 {
		return cfg, fmt.Errorf("VG_JWT_SECRET must be at least 16 characters")
	}
	return cfg, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}

func envList(key string, def []string) []string {
	raw := env(key, "")
	if raw == "" {
		return def
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "api"
	}
	return h
}
