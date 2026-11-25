package handler_test

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *logger.Logger {
	return &logger.Logger{Logger: log.New(io.Discard, "", 0), Level: "error"}
}

// newApp wires the handlers with the given token → tenant pairs.
func newApp(t *testing.T, tokens map[string]string) (*fiber.App, *metrics.Metrics) {
	t.Helper()
	l := discardLogger()
	m := metrics.New()
	ts := auth.NewTokenStore(l)
	for token, tenant := range tokens {
		require.NoError(t, ts.Add(token, tenant))
	}
	h := handler.New(ts, l, m)

	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(l, m)})
	app.Get("/validate-token", h.ValidateToken)
	app.Get("/healthz", h.Health)
	app.Get("/metrics", h.Metrics)
	return app, m
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

func TestValidateToken_Valid(t *testing.T) {
	app, m := newApp(t, map[string]string{"test-token": "test-tenant"})

	resp := validate(t, app, "Bearer test-token")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "test-tenant", resp.Header.Get("X-Scope-OrgID"))
	total, success, failed, _ := m.Get()
	assert.Equal(t, [3]uint64{1, 1, 0}, [3]uint64{total, success, failed})
}

func TestValidateToken_CaseInsensitiveBearerAndWhitespace(t *testing.T) {
	app, _ := newApp(t, map[string]string{"test-token": "tenant-test"})

	resp := validate(t, app, "bearer   test-token  ")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-test", resp.Header.Get("X-Scope-OrgID"))
}

func TestValidateToken_Rejected(t *testing.T) {
	app, _ := newApp(t, map[string]string{"valid-token": "tenant-1"})

	tests := []struct {
		name, authorization, wantError string
	}{
		{"invalid token", "Bearer invalid-token", "invalid token"},
		{"missing header", "", "missing Authorization header"},
		{"basic scheme", "Basic invalid-format", "invalid Authorization header"},
		{"bearer without token", "Bearer", "invalid Authorization header"},
		{"bearer with only spaces", "Bearer    ", "invalid Authorization header"},
		{"truncated scheme", "Bear", "invalid Authorization header"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := validate(t, app, tt.authorization)

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Empty(t, resp.Header.Get("X-Scope-OrgID"))
			assert.Equal(t, tt.wantError, errorBody(t, resp))
		})
	}
}

func TestHealth(t *testing.T) {
	app, _ := newApp(t, nil)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil), -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "healthy", body["status"])
	assert.NotEmpty(t, body["uptime"])
	assert.NotEmpty(t, body["timestamp"])
	assert.Equal(t, "1.0.0", body["version"])
}

func TestMetrics(t *testing.T) {
	app, _ := newApp(t, map[string]string{"token-1": "tenant-1", "token-2": "tenant-2"})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil), -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", resp.Header.Get("Content-Type"))

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	output := string(raw)
	assert.Contains(t, output, "go_grafana_auth_requests_total")
	assert.Contains(t, output, "go_grafana_auth_requests_success_total")
	assert.Contains(t, output, "go_grafana_auth_requests_failed_total")
	assert.Contains(t, output, "go_grafana_auth_uptime_seconds")
	assert.Contains(t, output, "go_grafana_auth_tokens_configured 2")
}

func TestCustomErrorHandler(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(discardLogger(), metrics.New())})
	app.Get("/bad", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "bad request")
	})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return errors.New("dial tcp 10.0.0.5:5432: connection refused")
	})

	t.Run("fiber error keeps status and message", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/bad", nil), -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "bad request", errorBody(t, resp))
	})

	t.Run("internal error is hidden", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil), -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "internal server error", errorBody(t, resp))
	})
}
