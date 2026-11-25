package middleware_test

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *logger.Logger {
	return &logger.Logger{Logger: log.New(io.Discard, "", 0), Level: "error"}
}

func get(t *testing.T, app *fiber.App, path, authorization string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	return resp.StatusCode
}

// Only failed attempts count towards the limit.
func TestRateLimiter_CountsOnlyFailedRequests(t *testing.T) {
	m := metrics.New()
	app := fiber.New()
	app.Get("/auth", middleware.RateLimiter(discardLogger(), m, 3, 10*time.Second), func(c *fiber.Ctx) error {
		if c.Get("Authorization") == "Bearer good" {
			return c.SendStatus(fiber.StatusOK)
		}
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	for i := 0; i < 5; i++ {
		assert.Equal(t, http.StatusOK, get(t, app, "/auth", "Bearer good"), "valid request %d", i+1)
	}
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusUnauthorized, get(t, app, "/auth", "Bearer wrong"), "failed attempt %d", i+1)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":"rate limit exceeded"}`, string(body))

	_, _, failed, _ := m.Get()
	assert.Equal(t, uint64(1), failed, "LimitReached counts as a failure")
}

func TestRateLimiter_Defaults(t *testing.T) {
	app := fiber.New()
	app.Get("/auth", middleware.RateLimiter(discardLogger(), metrics.New(), 0, 0), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	for i := 0; i < 100; i++ {
		require.Equal(t, http.StatusUnauthorized, get(t, app, "/auth", ""), "attempt %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, get(t, app, "/auth", ""), "default max is 100")
}

// /healthz and /metrics are not logged.
func TestRequestLogger_SkipsProbes(t *testing.T) {
	var buf bytes.Buffer
	l := &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0", Level: "info"}

	app := fiber.New()
	app.Use(middleware.RequestLogger(l))
	ok := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	app.Get("/healthz", ok)
	app.Get("/metrics", ok)
	app.Get("/api/data", ok)

	get(t, app, "/healthz", "")
	get(t, app, "/metrics", "")
	assert.Zero(t, buf.Len())

	get(t, app, "/api/data", "")
	assert.Contains(t, buf.String(), "/api/data")
}
