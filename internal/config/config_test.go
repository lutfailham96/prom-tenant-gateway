package config_test

import (
	"testing"
	"time"

	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/stretchr/testify/assert"
)

// clearConfigEnv blanks all variables read by config.Load; empty values are
// treated as unset.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PORT", "HTTP_PORT", "RATE_LIMIT_ENABLED", "RATE_LIMIT_MAX", "RATE_LIMIT_WINDOW", "LOG_LEVEL", "TRUSTED_PROXIES"} {
		t.Setenv(key, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg := config.Load()

	assert.Equal(t, "8080", cfg.Port)
	assert.True(t, cfg.RateLimitEnabled)
	assert.Equal(t, 100, cfg.RateLimitMax)
	assert.Equal(t, time.Minute, cfg.RateLimitWindow)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoad_PortPrecedenceAndLogLevel(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("HTTP_PORT", "8081")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := config.Load()
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, "debug", cfg.LogLevel)

	t.Setenv("PORT", "")
	assert.Equal(t, "8081", config.Load().Port, "falls back to HTTP_PORT")
}

func TestLoad_RateLimit(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("RATE_LIMIT_ENABLED", "false")
	t.Setenv("RATE_LIMIT_MAX", "7")
	t.Setenv("RATE_LIMIT_WINDOW", "10s")

	cfg := config.Load()

	assert.False(t, cfg.RateLimitEnabled)
	assert.Equal(t, 7, cfg.RateLimitMax)
	assert.Equal(t, 10*time.Second, cfg.RateLimitWindow)
}

func TestLoad_TrustedProxies(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.0/8, ,192.168.1.1 ")

	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.1"}, config.Load().TrustedProxies)
}
