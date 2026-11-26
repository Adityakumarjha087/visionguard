package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultsAndOverrides(t *testing.T) {
	t.Setenv("VG_PORT", "9999")
	t.Setenv("VG_ACCESS_TTL", "5m")
	t.Setenv("VG_CORS_ORIGINS", "http://a, http://b ,")
	t.Setenv("VG_RATE_LIMIT_RPS", "2.5")
	t.Setenv("VG_INFERENCE_URL", "http://inference:8000/")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9999, cfg.Port)
	assert.Equal(t, 5*time.Minute, cfg.AccessTTL)
	assert.Equal(t, []string{"http://a", "http://b"}, cfg.CORSOrigins)
	assert.Equal(t, 2.5, cfg.RateLimitRPS)
	assert.Equal(t, "http://inference:8000", cfg.InferenceURL)
	assert.NotEmpty(t, cfg.JWTSecret)
}

func TestProductionRequiresSecret(t *testing.T) {
	t.Setenv("VG_ENV", "production")
	_, err := Load()
	assert.Error(t, err)
	t.Setenv("VG_JWT_SECRET", "short")
	_, err = Load()
	assert.Error(t, err)
	t.Setenv("VG_JWT_SECRET", "a-long-enough-production-secret")
	_, err = Load()
	assert.NoError(t, err)
}
