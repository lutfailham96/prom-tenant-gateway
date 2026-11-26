package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newApp wires the handlers with the given token → tenant pairs.
func newApp(t *testing.T, tokens map[string]string) *fiber.App {
	t.Helper()
	l := logger.Discard()
	m := metrics.New("test")
	ts := auth.NewTokenStore(l)
	for token, tenant := range tokens {
		require.NoError(t, ts.Add(token, tenant))
	}
	m.SetTenants(ts.Tenants(), ts.Count())
	h := handler.New(ts, m, "9.9.9-test")

	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(l)})
	app.Get("/validate-token", h.ValidateToken)
	app.Get("/healthz", h.Health)
	app.Get("/metrics", h.Metrics)
	return app
}

func validate(t *testing.T, app *fiber.App, authorization string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/validate-token", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	return resp
}

func errorBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body["error"]
}

func scrape(t *testing.T, app *fiber.App) string {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil), -1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(raw)
}

func TestValidateToken_Valid(t *testing.T) {
	app := newApp(t, map[string]string{"test-token": "test-tenant"})

	resp := validate(t, app, "Bearer test-token")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "test-tenant", resp.Header.Get(handler.HeaderScopeOrgID))
	assert.Contains(t, scrape(t, app), `prom_tenant_gateway_auth_requests_total{result="success",tenant="test-tenant"} 1`)
}

func TestValidateToken_CaseInsensitiveBearerAndWhitespace(t *testing.T) {
	app := newApp(t, map[string]string{"test-token": "tenant-test"})

	resp := validate(t, app, "bearer   test-token  ")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-test", resp.Header.Get(handler.HeaderScopeOrgID))
}

func TestValidateToken_Rejected(t *testing.T) {
	tests := []struct {
		name, authorization, wantError, wantResult string
	}{
		{"invalid token", "Bearer invalid-token", "invalid token", metrics.ResultInvalidToken},
		{"missing header", "", "missing Authorization header", metrics.ResultMissingHeader},
		{"basic scheme", "Basic invalid-format", "invalid Authorization header", metrics.ResultInvalidHeader},
		{"bearer without token", "Bearer", "invalid Authorization header", metrics.ResultInvalidHeader},
		{"bearer with only spaces", "Bearer    ", "invalid Authorization header", metrics.ResultInvalidHeader},
		{"truncated scheme", "Bear", "invalid Authorization header", metrics.ResultInvalidHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp(t, map[string]string{"valid-token": "tenant-1"})

			resp := validate(t, app, tt.authorization)

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
			assert.Empty(t, resp.Header.Get(handler.HeaderScopeOrgID))
			assert.Equal(t, tt.wantError, errorBody(t, resp))
			assert.Contains(t, scrape(t, app), `prom_tenant_gateway_auth_requests_total{result="`+tt.wantResult+`",tenant=""} 1`)
		})
	}
}

// The handler stores the auth result for the request log.
func TestValidateToken_SetsAuthResultLocal(t *testing.T) {
	ts := auth.NewTokenStore(logger.Discard())
	require.NoError(t, ts.Add("good", "a"))
	h := handler.New(ts, metrics.New("test"), "9.9.9-test")

	var got []string
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		result, _ := c.Locals(metrics.LocalsAuthResult).(string)
		got = append(got, result)
		return err
	})
	app.Get("/validate-token", h.ValidateToken)

	validate(t, app, "Bearer good")
	validate(t, app, "Bearer bad")
	validate(t, app, "")
	validate(t, app, "Basic x")

	assert.Equal(t, []string{
		metrics.ResultSuccess, metrics.ResultInvalidToken, metrics.ResultMissingHeader, metrics.ResultInvalidHeader,
	}, got)
}

// limitedApp wires ValidateToken with a failure limiter keyed on a constant,
// i.e. every request comes from the same client.
func limitedApp(t *testing.T, max int) (*fiber.App, *metrics.Metrics) {
	t.Helper()
	ts := auth.NewTokenStore(logger.Discard())
	require.NoError(t, ts.Add("good", "tenant-a"))
	m := metrics.New("test")
	m.SetTenants(ts.Tenants(), ts.Count())
	l, err := ratelimit.New(max, time.Minute)
	require.NoError(t, err)
	h := handler.New(ts, m, "9.9.9-test",
		handler.WithFailureLimiter(l, func(*fiber.Ctx) string { return "client" }))

	app := fiber.New()
	app.Get("/validate-token", h.ValidateToken)
	return app, m
}

func TestValidateToken_FailureLimit(t *testing.T) {
	app, _ := limitedApp(t, 3)

	// Missing, malformed and invalid credentials all count as failures.
	assert.Equal(t, http.StatusUnauthorized, validate(t, app, "").StatusCode)
	assert.Equal(t, http.StatusUnauthorized, validate(t, app, "Basic x").StatusCode)
	assert.Equal(t, http.StatusUnauthorized, validate(t, app, "Bearer bad").StatusCode)

	resp := validate(t, app, "Bearer bad")
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "rate limit exceeded", errorBody(t, resp))
	retry, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	require.NoError(t, err)
	assert.True(t, retry >= 1 && retry <= 60, "Retry-After=%d", retry)

	assert.Equal(t, http.StatusTooManyRequests, validate(t, app, "").StatusCode)
}

// A valid token is accepted while the client is throttled, and does not
// lift the throttle for further failures.
func TestValidateToken_ValidTokenNotLimited(t *testing.T) {
	app, m := limitedApp(t, 1)

	assert.Equal(t, http.StatusUnauthorized, validate(t, app, "Bearer bad").StatusCode)
	assert.Equal(t, http.StatusTooManyRequests, validate(t, app, "Bearer bad").StatusCode)

	for i := 0; i < 3; i++ {
		resp := validate(t, app, "Bearer good")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "tenant-a", resp.Header.Get(handler.HeaderScopeOrgID))
		assert.Empty(t, resp.Header.Get("Retry-After"))
	}

	assert.Equal(t, http.StatusTooManyRequests, validate(t, app, "Bearer bad").StatusCode)

	// Each request is counted under exactly one result.
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := rec.Body.String()
	assert.Contains(t, out, `prom_tenant_gateway_auth_requests_total{result="invalid_token",tenant=""} 1`)
	assert.Contains(t, out, `prom_tenant_gateway_auth_requests_total{result="rate_limited",tenant=""} 2`)
	assert.Contains(t, out, `prom_tenant_gateway_auth_requests_total{result="success",tenant="tenant-a"} 3`)
}

// The rate-limited result is visible to the request log.
func TestValidateToken_RateLimitedAuthResultLocal(t *testing.T) {
	ts := auth.NewTokenStore(logger.Discard())
	l, err := ratelimit.New(1, time.Minute)
	require.NoError(t, err)
	h := handler.New(ts, metrics.New("test"), "9.9.9-test",
		handler.WithFailureLimiter(l, func(*fiber.Ctx) string { return "client" }))

	var got []string
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		result, _ := c.Locals(metrics.LocalsAuthResult).(string)
		got = append(got, result)
		return err
	})
	app.Get("/validate-token", h.ValidateToken)

	validate(t, app, "Bearer bad")
	validate(t, app, "Bearer bad")
	assert.Equal(t, []string{metrics.ResultInvalidToken, metrics.ResultRateLimited}, got)
}

func TestHealth(t *testing.T) {
	app := newApp(t, nil)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil), -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "healthy", body["status"])
	assert.NotEmpty(t, body["uptime"])
	assert.NotEmpty(t, body["timestamp"])
	assert.Equal(t, "9.9.9-test", body["version"])
}

func TestMetrics_Exposition(t *testing.T) {
	app := newApp(t, map[string]string{"token-1": "tenant-1", "token-2": "tenant-2"})

	out := scrape(t, app)

	assert.Contains(t, out, "prom_tenant_gateway_tokens_configured 2")
	assert.Contains(t, out, `prom_tenant_gateway_auth_requests_total{result="success",tenant="tenant-1"} 0`)
	assert.Contains(t, out, "prom_tenant_gateway_build_info")
	assert.Contains(t, out, "process_start_time_seconds")
	assert.NotContains(t, out, "go_grafana_auth_")
}

func TestCustomErrorHandler(t *testing.T) {
	var buf bytes.Buffer
	l := logger.New(&buf, "test", "1.0.0", slog.LevelInfo)
	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(l)})
	app.Get("/bad", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "bad request")
	})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return errors.New("dial tcp 10.0.0.5:5432: connection refused")
	})

	t.Run("fiber error keeps status and message, not logged at info", func(t *testing.T) {
		buf.Reset()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/bad", nil), -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "bad request", errorBody(t, resp))
		assert.Zero(t, buf.Len(), "4xx is logged at debug only")
	})

	t.Run("internal error is hidden from client but logged", func(t *testing.T) {
		buf.Reset()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil), -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "internal server error", errorBody(t, resp))
		assert.Contains(t, buf.String(), `"level":"error"`)
		assert.Contains(t, buf.String(), "connection refused")
	})
}
