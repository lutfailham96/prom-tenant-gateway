package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		out = append(out, entry)
	}
	return out
}

// observedApp mirrors the production middleware order: observer, recover,
// then routes, with the real error handler.
func observedApp(level slog.Level) (*fiber.App, *metrics.Metrics, *bytes.Buffer) {
	var buf bytes.Buffer
	l := logger.New(&buf, "test", "1.0.0", level)
	m := metrics.New("test")
	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(l)})
	app.Use(middleware.RequestObserver(l, m))
	app.Use(recover.New())
	return app, m, &buf
}

// Only failed attempts count towards the limit.
func TestRateLimiter_CountsOnlyFailedRequests(t *testing.T) {
	m := metrics.New("test")
	app := fiber.New()
	app.Get("/auth", middleware.RateLimiter(m, 3, 10*time.Second), func(c *fiber.Ctx) error {
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

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/auth", nil), -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":"rate limit exceeded"}`, string(body))

	assert.Contains(t, scrape(t, m), `prom_tenant_gateway_auth_requests_total{result="rate_limited",tenant=""} 1`)
}

func TestRateLimiter_Defaults(t *testing.T) {
	app := fiber.New()
	app.Get("/auth", middleware.RateLimiter(metrics.New("test"), 0, 0), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	for i := 0; i < 100; i++ {
		require.Equal(t, http.StatusUnauthorized, get(t, app, "/auth", ""), "attempt %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, get(t, app, "/auth", ""), "default max is 100")
}

// Successful requests are logged at debug, so they vanish at info level, but
// are still measured.
func TestRequestObserver_SuccessIsDebug(t *testing.T) {
	app, m, buf := observedApp(slog.LevelInfo)
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	assert.Equal(t, http.StatusOK, get(t, app, "/ok", ""))
	assert.Zero(t, buf.Len())
	assert.Contains(t, scrape(t, m), `prom_tenant_gateway_http_request_duration_seconds_count{code="200",method="GET",route="/ok"} 1`)
}

// A failed auth is logged once, at warn, with the auth result.
func TestRequestObserver_AuthFailureIsWarnWithResult(t *testing.T) {
	app, _, buf := observedApp(slog.LevelInfo)
	app.Get("/validate-token", func(c *fiber.Ctx) error {
		c.Locals(metrics.LocalsAuthResult, metrics.ResultInvalidToken)
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	get(t, app, "/validate-token", "Bearer x")

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "warn", lines[0]["level"])
	assert.Equal(t, "HTTP request", lines[0]["message"])
	assert.Equal(t, float64(401), lines[0]["status"])
	assert.Equal(t, metrics.ResultInvalidToken, lines[0]["auth_result"])
	assert.NotContains(t, lines[0], "tenant_id")
}

// At debug level a successful auth logs the tenant from X-Scope-OrgID.
func TestRequestObserver_LogsTenantOnSuccess(t *testing.T) {
	app, _, buf := observedApp(slog.LevelDebug)
	app.Get("/validate-token", func(c *fiber.Ctx) error {
		c.Locals(metrics.LocalsAuthResult, metrics.ResultSuccess)
		c.Set(handler.HeaderScopeOrgID, "tenant-a")
		return c.SendStatus(fiber.StatusOK)
	})

	get(t, app, "/validate-token", "Bearer x")

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "debug", lines[0]["level"])
	assert.Equal(t, "tenant-a", lines[0]["tenant_id"])
	assert.Equal(t, metrics.ResultSuccess, lines[0]["auth_result"])
}

// Errors returned by handlers are logged and measured with the status that
// was actually sent (previously always 200).
func TestRequestObserver_RecordsRealStatusForErrors(t *testing.T) {
	app, m, buf := observedApp(slog.LevelInfo)
	app.Get("/teapot", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusTeapot, "short and stout")
	})

	assert.Equal(t, http.StatusTeapot, get(t, app, "/teapot", ""))
	assert.Equal(t, http.StatusNotFound, get(t, app, "/does-not-exist", ""))

	lines := logLines(t, buf)
	require.Len(t, lines, 2)
	assert.Equal(t, float64(418), lines[0]["status"])
	assert.Equal(t, "info", lines[0]["level"])
	assert.Equal(t, float64(404), lines[1]["status"])
	assert.Equal(t, "info", lines[1]["level"], "404 from scanners is not an error")

	out := scrape(t, m)
	assert.Contains(t, out, `prom_tenant_gateway_http_request_duration_seconds_count{code="418",method="GET",route="/teapot"} 1`)
	assert.Contains(t, out, `prom_tenant_gateway_http_request_duration_seconds_count{code="404",method="GET",route="unmatched"} 1`)
	assert.NotContains(t, out, "/does-not-exist", "raw paths never become label values")
}

// Panics are recovered and observed as 500 at error level.
func TestRequestObserver_PanicIs500(t *testing.T) {
	app, m, buf := observedApp(slog.LevelInfo)
	app.Get("/panic", func(c *fiber.Ctx) error { panic("boom") })

	assert.Equal(t, http.StatusInternalServerError, get(t, app, "/panic", ""))

	var requestLines int
	for _, line := range logLines(t, buf) {
		assert.Equal(t, "error", line["level"])
		if line["message"] == "HTTP request" {
			requestLines++
			assert.Equal(t, float64(500), line["status"])
		}
	}
	assert.Equal(t, 1, requestLines)
	assert.Contains(t, scrape(t, m), `prom_tenant_gateway_http_request_duration_seconds_count{code="500",method="GET",route="/panic"} 1`)
}

// /healthz and /metrics are measured but not logged.
func TestRequestObserver_SkipsProbesInLog(t *testing.T) {
	app, m, buf := observedApp(slog.LevelDebug)
	ok := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	app.Get("/healthz", ok)
	app.Get("/metrics", ok)
	app.Get("/api/data", ok)

	get(t, app, "/healthz", "")
	get(t, app, "/metrics", "")
	assert.Zero(t, buf.Len())

	get(t, app, "/api/data", "")
	assert.Contains(t, buf.String(), "/api/data")
	assert.Contains(t, scrape(t, m), `route="/healthz"`)
}
