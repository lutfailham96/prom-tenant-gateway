package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthService_startup verifies service can be created and configured
func TestAuthService_startup(t *testing.T) {
	cfg := config.Config{
		Port:            "0",
		RateLimitMax:    10,
		RateLimitWindow: 1 * time.Second,
	}

	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})

	app.Use(middleware.RequestLogger(log))
	app.Use(middleware.RateLimiter(log, m, cfg.RateLimitMax, cfg.RateLimitWindow))

	h := handler.New(tokenStore, log, m)
	app.Get("/metrics", h.Metrics)
	app.Get("/healthz", h.Health)
	app.Get("/validate-token", h.ValidateToken)

	require.NotNil(t, app)
	require.NotNil(t, tokenStore)
	require.NotNil(t, h)
	assert.Equal(t, cfg.RateLimitMax, 10)
	assert.Equal(t, cfg.RateLimitWindow, 1*time.Second)
}

// TestLoadTokensFromEnv_newPattern tests TENANT_*_TOKEN pattern
func TestLoadTokensFromEnv_newPattern(t *testing.T) {
	os.Setenv("TENANT_A_TOKEN", "token-a-123")
	os.Setenv("TENANT_B_TOKEN", "token-b-456")
	os.Setenv("TENANT_PROD_TOKEN", "prod-secret")
	defer func() {
		os.Unsetenv("TENANT_A_TOKEN")
		os.Unsetenv("TENANT_B_TOKEN")
		os.Unsetenv("TENANT_PROD_TOKEN")
	}()

	log := logger.New("test-service", "1.0.0")
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.LoadFromEnv())

	assert.Equal(t, 3, tokenStore.Count())
	assert.Equal(t, "a", tokenStore.Tokens()["token-a-123"])
	assert.Equal(t, "b", tokenStore.Tokens()["token-b-456"])
	assert.Equal(t, "prod", tokenStore.Tokens()["prod-secret"])
}

// TestLoadTokensFromEnv_legacyPattern tests VALID_TOKENS_JSON pattern
func TestLoadTokensFromEnv_legacyPattern(t *testing.T) {
	os.Setenv("VALID_TOKENS_JSON", `{"tenant-legacy": "legacy-token-1", "another-tenant": "legacy-token-2"}`)
	defer os.Unsetenv("VALID_TOKENS_JSON")

	log := logger.New("test-service", "1.0.0")
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.LoadFromEnv())

	assert.Equal(t, 2, tokenStore.Count())
	assert.Equal(t, "tenant-legacy", tokenStore.Tokens()["legacy-token-1"])
	assert.Equal(t, "another-tenant", tokenStore.Tokens()["legacy-token-2"])
}

// TestLoadTokensFromEnv_combinedPatterns tests both patterns together
func TestLoadTokensFromEnv_combinedPatterns(t *testing.T) {
	os.Setenv("TENANT_A_TOKEN", "new-token-a")
	os.Setenv("VALID_TOKENS_JSON", `{"legacy-tenant": "legacy-token"}`)
	defer func() {
		os.Unsetenv("TENANT_A_TOKEN")
		os.Unsetenv("VALID_TOKENS_JSON")
	}()

	log := logger.New("test-service", "1.0.0")
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.LoadFromEnv())

	assert.Equal(t, 2, tokenStore.Count())
	assert.Equal(t, "a", tokenStore.Tokens()["new-token-a"])
	assert.Equal(t, "legacy-tenant", tokenStore.Tokens()["legacy-token"])
}

// TestLoadTokensFromEnv_empty fails when no tokens configured
func TestLoadTokensFromEnv_empty(t *testing.T) {
	os.Unsetenv("TENANT_A_TOKEN")
	os.Unsetenv("VALID_TOKENS_JSON")

	log := logger.New("test-service", "1.0.0")
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.LoadFromEnv())

	assert.Equal(t, 0, tokenStore.Count())
}

// TestValidateTokenHandler_validToken returns 200 and sets X-Scope-OrgID
func TestValidateTokenHandler_validToken(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.Add("test-token", "test-tenant"))

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/validate-token", h.ValidateToken)

	req := httptest.NewRequest("GET", "/validate-token", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "test-tenant", resp.Header.Get("X-Scope-OrgID"))
}

// TestValidateTokenHandler_invalidToken returns 401
func TestValidateTokenHandler_invalidToken(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.Add("valid-token", "tenant-1"))

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/validate-token", h.ValidateToken)

	req := httptest.NewRequest("GET", "/validate-token", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "invalid token", body["error"])
}

// TestValidateTokenHandler_missingAuthHeader returns 401
func TestValidateTokenHandler_missingAuthHeader(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/validate-token", h.ValidateToken)

	req := httptest.NewRequest("GET", "/validate-token", nil)
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "missing Authorization header", body["error"])
}

// TestValidateTokenHandler_malformedAuthHeader returns 401
func TestValidateTokenHandler_malformedAuthHeader(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/validate-token", h.ValidateToken)

	req := httptest.NewRequest("GET", "/validate-token", nil)
	req.Header.Set("Authorization", "Basic invalid-format")
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "invalid Authorization header", body["error"])
}

// TestHealthHandler returns healthy status
func TestHealthHandler(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/healthz", h.Health)

	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "healthy", body["status"])
	assert.NotEmpty(t, body["uptime"])
	assert.NotEmpty(t, body["timestamp"])
	assert.Equal(t, "1.0.0", body["version"])
}

// TestMetricsHandler returns Prometheus format metrics
func TestMetricsHandler(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.Add("token-1", "tenant-1"))
	require.NoError(t, tokenStore.Add("token-2", "tenant-2"))

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/metrics", h.Metrics)

	req := httptest.NewRequest("GET", "/metrics", nil)
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", resp.Header.Get("Content-Type"))

	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	output := body.String()

	assert.Contains(t, output, "go_grafana_auth_requests_total")
	assert.Contains(t, output, "go_grafana_auth_requests_success_total")
	assert.Contains(t, output, "go_grafana_auth_requests_failed_total")
	assert.Contains(t, output, "go_grafana_auth_uptime_seconds")
	assert.Contains(t, output, "go_grafana_auth_tokens_configured 2")
}

// TestRateLimiting triggers 429 after too many failed attempts
func TestRateLimiting(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.Add("test-token", "test-tenant"))

	cfg := config.Config{
		RateLimitMax:    3,
		RateLimitWindow: 10 * time.Second,
	}

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Use(middleware.RateLimiter(log, m, cfg.RateLimitMax, cfg.RateLimitWindow))
	app.Get("/validate-token", h.ValidateToken)

	// Successful requests must not count towards the limit
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/validate-token", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "Valid request %d should succeed", i+1)
	}

	// 3 failed attempts are allowed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/validate-token", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Failed attempt %d should be 401", i+1)
	}

	// 4th failed attempt - should be rate limited
	req := httptest.NewRequest("GET", "/validate-token", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "rate limit exceeded", body["error"])
}

// TestGracefulShutdown tests shutdown via context cancellation
func TestGracefulShutdown(t *testing.T) {
	log := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)
	require.NoError(t, tokenStore.Add("test-token", "test-tenant"))

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
	})
	app.Get("/healthz", h.Health)

	// Start server in background
	_, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- app.Listen(":0")
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is running by making a request
	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Trigger shutdown using app.Shutdown()
	cancel()
	_ = app.Shutdown()

	// Wait for shutdown
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// TestExtractTenantIDFromEnvVarName tests the tenant ID extraction logic
func TestExtractTenantIDFromEnvVarName(t *testing.T) {
	tests := []struct {
		envVar     string
		expectedID string
	}{
		{"TENANT_A_TOKEN", "a"},
		{"TENANT_B_TOKEN", "b"},
		{"TENANT_PROD_TOKEN", "prod"},
		{"TENANT_STAGING_TOKEN", "staging"},
		{"TENANT_my_custom_TENANT_TOKEN", "my_custom_tenant"},
	}

	for _, tt := range tests {
		t.Run(tt.envVar, func(t *testing.T) {
			tenantID := auth.ExtractTenantIDFromEnvVar(tt.envVar)
			assert.Equal(t, tt.expectedID, tenantID)
		})
	}
}

// TestJSONLogger_format verifies log output is valid JSON
func TestJSONLogger_format(t *testing.T) {
	var buf bytes.Buffer
	log := &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0"}

	log.Info("test message", map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	})

	var entry map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	assert.Equal(t, "info", entry["level"])
	assert.Equal(t, "test message", entry["message"])
	assert.Equal(t, "test", entry["service"])
	assert.Equal(t, "1.0.0", entry["version"])
	assert.Equal(t, "value1", entry["key1"])
	assert.Equal(t, float64(123), entry["key2"])
	assert.NotEmpty(t, entry["timestamp"])
}

// TestCustomErrorHandler returns JSON error
func TestCustomErrorHandler(t *testing.T) {
	var buf bytes.Buffer
	log := &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0"}
	m := metrics.New()

	errorHandler := handler.CustomErrorHandler(log, m)

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Get("/error", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "bad request")
	})

	req := httptest.NewRequest("GET", "/error", nil)
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "bad request", body["error"])
}

// TestMetrics_increment verifies metrics counters
func TestMetrics_increment(t *testing.T) {
	m := metrics.New()

	total, success, failed, _ := m.Get()
	assert.Equal(t, uint64(0), total)
	assert.Equal(t, uint64(0), success)
	assert.Equal(t, uint64(0), failed)

	m.IncTotal()
	m.IncTotal()
	m.IncSuccess()
	m.IncFailed()

	total, success, failed, _ = m.Get()
	assert.Equal(t, uint64(2), total)
	assert.Equal(t, uint64(1), success)
	assert.Equal(t, uint64(1), failed)
}

// TestConfig_PortAndLogLevel verifies PORT precedence and LOG_LEVEL
func TestConfig_PortAndLogLevel(t *testing.T) {
	// Test PORT precedence over HTTP_PORT
	os.Setenv("PORT", "9090")
	os.Setenv("HTTP_PORT", "8080")
	os.Setenv("LOG_LEVEL", "debug")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("HTTP_PORT")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg := config.Load()
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, "debug", cfg.LogLevel)

	// Test fallback to HTTP_PORT when PORT is empty
	os.Unsetenv("PORT")
	cfgFallback := config.Load()
	assert.Equal(t, "8080", cfgFallback.Port)
}

// TestLogger_LevelFiltering verifies logger drops messages below configured level
func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0", Level: "info"}

	// Debug message should be dropped
	l.Debug("dropped debug message", nil)
	assert.Equal(t, 0, buf.Len())

	// Info message should be printed
	l.Info("allowed info message", nil)
	assert.Contains(t, buf.String(), "allowed info message")

	// Set level to debug
	buf.Reset()
	l.SetLevel("debug")
	l.Debug("allowed debug message", nil)
	assert.Contains(t, buf.String(), "allowed debug message")
}

// TestRequestLogger_SkipProbes verifies /healthz and /metrics are not logged
func TestRequestLogger_SkipProbes(t *testing.T) {
	var buf bytes.Buffer
	l := &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0", Level: "info"}

	app := fiber.New()
	app.Use(middleware.RequestLogger(l))
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	app.Get("/metrics", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	app.Get("/api/data", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	// /healthz should not be logged
	req := httptest.NewRequest("GET", "/healthz", nil)
	_, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, 0, buf.Len())

	// /metrics should not be logged
	req = httptest.NewRequest("GET", "/metrics", nil)
	_, err = app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, 0, buf.Len())

	// regular route should be logged
	req = httptest.NewRequest("GET", "/api/data", nil)
	_, err = app.Test(req, -1)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "/api/data")
}

// TestValidateTokenHandler_CaseInsensitiveBearer verifies lowercase bearer and whitespace handling
func TestValidateTokenHandler_CaseInsensitiveBearer(t *testing.T) {
	l := logger.New("test-service", "1.0.0")
	m := metrics.New()
	tokenStore := auth.NewTokenStore(l)
	require.NoError(t, tokenStore.Add("test-token", "tenant-test"))

	h := handler.New(tokenStore, l, m)

	app := fiber.New()
	app.Get("/validate-token", h.ValidateToken)

	req := httptest.NewRequest("GET", "/validate-token", nil)
	req.Header.Set("Authorization", "bearer   test-token  ")
	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-test", resp.Header.Get("X-Scope-OrgID"))
}

func discardLogger() *logger.Logger {
	return &logger.Logger{Logger: log.New(io.Discard, "", 0), Level: "error"}
}

// TestTokenStore_TrimsWhitespace verifies tokens from files/secrets with a
// trailing newline still match the trimmed bearer token
func TestTokenStore_TrimsWhitespace(t *testing.T) {
	t.Setenv("TENANT_X_TOKEN", "  secret-x\n")

	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.LoadFromEnv())

	tenant, ok := ts.Validate("secret-x")
	assert.True(t, ok)
	assert.Equal(t, "x", tenant)
}

// TestTokenStore_IgnoresMalformedEnvNames verifies TENANT_TOKEN and
// TENANT__TOKEN are not treated as tenants
func TestTokenStore_IgnoresMalformedEnvNames(t *testing.T) {
	t.Setenv("TENANT_TOKEN", "bare")
	t.Setenv("TENANT__TOKEN", "empty-id")

	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.LoadFromEnv())

	_, ok := ts.Validate("bare")
	assert.False(t, ok)
	_, ok = ts.Validate("empty-id")
	assert.False(t, ok)
}

// TestTokenStore_RejectsInvalidTenantID verifies invalid tenant IDs fail loading
func TestTokenStore_RejectsInvalidTenantID(t *testing.T) {
	t.Setenv("VALID_TOKENS_JSON", `{"a|b": "federated-token", "good": "good-token"}`)

	ts := auth.NewTokenStore(discardLogger())
	err := ts.LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a|b")
	_, ok := ts.Validate("federated-token")
	assert.False(t, ok)
	_, ok = ts.Validate("good-token")
	assert.True(t, ok, "valid entries are still loaded")
}

// TestTokenStore_RejectsSharedToken verifies one token cannot map to two tenants
func TestTokenStore_RejectsSharedToken(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.Add("same", "tenant-a"))
	assert.Error(t, ts.Add("same", "tenant-b"))
	assert.NoError(t, ts.Add("same", "tenant-a"), "re-adding the same mapping is fine")
}

// TestValidateTenantID covers Mimir tenant ID rules
func TestValidateTenantID(t *testing.T) {
	valid := []string{"a", "prod", "Tenant-1", "team_x.prod", "a!*'()"}
	for _, id := range valid {
		assert.NoError(t, auth.ValidateTenantID(id), id)
	}
	invalid := []string{"", ".", "..", "a|b", "has space", "slash/x", "ünï", string(make([]byte, 151))}
	for _, id := range invalid {
		assert.Error(t, auth.ValidateTenantID(id), "%q", id)
	}
}

// TestTokenStore_TokensReturnsCopy verifies callers cannot mutate the store
func TestTokenStore_TokensReturnsCopy(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.Add("tok", "a"))

	ts.Tokens()["injected"] = "evil"

	_, ok := ts.Validate("injected")
	assert.False(t, ok)
}

// TestValidateTokenHandler_EmptyBearer returns 401 for a bearer scheme without token
func TestValidateTokenHandler_EmptyBearer(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	h := handler.New(ts, discardLogger(), metrics.New())
	app := fiber.New()
	app.Get("/validate-token", h.ValidateToken)

	for _, header := range []string{"Bearer", "Bearer    ", "Bear"} {
		req := httptest.NewRequest("GET", "/validate-token", nil)
		req.Header.Set("Authorization", header)
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%q", header)
	}
}

// TestCustomErrorHandler_HidesInternalErrors verifies non-fiber errors return a generic 500
func TestCustomErrorHandler_HidesInternalErrors(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: handler.CustomErrorHandler(discardLogger(), metrics.New())})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return errors.New("dial tcp 10.0.0.5:5432: connection refused")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil), -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "internal server error", body["error"])
}

// newRateLimitedApp builds an app configured like main.go's proxy handling
func newRateLimitedApp(t *testing.T, trusted []string) *fiber.App {
	t.Helper()
	l := discardLogger()
	m := metrics.New()
	ts := auth.NewTokenStore(l)
	require.NoError(t, ts.Add("good", "a"))
	h := handler.New(ts, l, m)

	app := fiber.New(fiber.Config{
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          trusted,
		EnableIPValidation:      true,
	})
	app.Get("/validate-token", middleware.RateLimiter(l, m, 2, time.Minute), h.ValidateToken)
	return app
}

func failedAttemptCodes(t *testing.T, app *fiber.App, n int) []int {
	t.Helper()
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest("GET", "/validate-token", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i))
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		codes = append(codes, resp.StatusCode)
	}
	return codes
}

// TestRateLimiting_SpoofedXFFFromUntrustedPeer verifies X-Forwarded-For from an
// untrusted peer cannot be used to get a fresh rate limit bucket per request
func TestRateLimiting_SpoofedXFFFromUntrustedPeer(t *testing.T) {
	app := newRateLimitedApp(t, nil) // app.Test peer is 0.0.0.0, not trusted

	codes := failedAttemptCodes(t, app, 4)
	assert.Equal(t, []int{401, 401, 429, 429}, codes)
}

// TestRateLimiting_XFFFromTrustedProxy verifies client IPs from a trusted proxy
// get separate buckets
func TestRateLimiting_XFFFromTrustedProxy(t *testing.T) {
	app := newRateLimitedApp(t, []string{"0.0.0.0"}) // app.Test peer address

	codes := failedAttemptCodes(t, app, 4)
	assert.Equal(t, []int{401, 401, 401, 401}, codes)
}

// TestConfig_TrustedProxies verifies comma-separated parsing
func TestConfig_TrustedProxies(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.0/8, ,192.168.1.1 ")
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.1"}, config.Load().TrustedProxies)

	t.Setenv("TRUSTED_PROXIES", "")
	assert.Empty(t, config.Load().TrustedProxies)
}
