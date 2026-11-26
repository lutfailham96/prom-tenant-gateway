package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// app.Test serves requests from this peer address.
const testPeerIP = "0.0.0.0"

func testConfig() config.Config {
	return config.Config{
		RateLimitEnabled: true,
		RateLimitMax:     2,
		RateLimitWindow:  time.Minute,
	}
}

func newTestApp(t *testing.T, cfg config.Config) *fiber.App {
	t.Helper()
	l := logger.Discard()
	ts := auth.NewTokenStore(l)
	require.NoError(t, ts.Add("good-token", "tenant-a"))
	m := metrics.New("test")
	m.SetTenants(ts.Tenants(), ts.Count())
	app, err := newApp(cfg, l, m, ts)
	require.NoError(t, err)
	return app
}

func do(t *testing.T, app *fiber.App, req *http.Request) *http.Response {
	t.Helper()
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	return resp
}

func authRequest(token, xff string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/validate-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return req
}

func TestNewApp_Routes(t *testing.T) {
	app := newTestApp(t, testConfig())

	tests := []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/metrics", http.StatusOK},
		{"/dashboard", http.StatusNotFound},
		{"/unknown", http.StatusNotFound},
	}
	for _, tt := range tests {
		resp := do(t, app, httptest.NewRequest(http.MethodGet, tt.path, nil))
		assert.Equal(t, tt.want, resp.StatusCode, tt.path)
	}

	resp := do(t, app, authRequest("good-token", ""))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-a", resp.Header.Get("X-Scope-OrgID"))
}

// startServer serves app on a real loopback listener and returns its base URL.
// The body limit is enforced while reading from the connection, so it can only
// be observed over a real socket (app.Test surfaces it as an error instead).
func startServer(t *testing.T, app *fiber.App) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- app.Listener(ln) }()

	base := "http://" + ln.Addr().String()
	require.Eventually(t, func() bool {
		resp, err := testClient.Get(base + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)
	return base, errCh
}

func TestNewApp_BodyLimit(t *testing.T) {
	app := newTestApp(t, testConfig())
	base, _ := startServer(t, app)
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(time.Second) })

	send := func(size int) (int, string) {
		req, err := http.NewRequest(http.MethodGet, base+"/validate-token", bytes.NewReader(make([]byte, size)))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer good-token")
		resp, err := testClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	code, _ := send(bodyLimit)
	assert.Equal(t, http.StatusOK, code)

	code, body := send(bodyLimit + 1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, code)
	assert.JSONEq(t, `{"error":"Request Entity Too Large"}`, body)
}

// failedAttempts sends n failed auth attempts, each with a different
// X-Forwarded-For client IP, and returns the status codes.
func failedAttempts(t *testing.T, app *fiber.App, n int) []int {
	t.Helper()
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		codes = append(codes, do(t, app, authRequest("wrong", "203.0.113."+strconv.Itoa(i))).StatusCode)
	}
	return codes
}

// X-Forwarded-For from an untrusted peer cannot buy a fresh rate-limit
// bucket per request.
func TestNewApp_SpoofedXFFFromUntrustedPeer(t *testing.T) {
	app := newTestApp(t, testConfig())

	assert.Equal(t, []int{401, 401, 429, 429}, failedAttempts(t, app, 4))
}

// Client IPs reported by a trusted proxy get separate buckets.
func TestNewApp_XFFFromTrustedProxy(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxies = []string{testPeerIP}
	app := newTestApp(t, cfg)

	assert.Equal(t, []int{401, 401, 401, 401}, failedAttempts(t, app, 4))
}

// A valid token from a throttled client IP is still accepted; failures from
// that IP stay throttled.
func TestNewApp_ValidTokenNotBlockedWhileLimited(t *testing.T) {
	app := newTestApp(t, testConfig())

	assert.Equal(t, []int{401, 401, 429}, failedAttempts(t, app, 3))

	resp := do(t, app, authRequest("good-token", ""))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-a", resp.Header.Get("X-Scope-OrgID"))

	assert.Equal(t, http.StatusTooManyRequests, do(t, app, authRequest("wrong", "")).StatusCode)
}

// A trusted proxy that appends to X-Forwarded-For cannot be used to pick a
// fresh bucket per request: forged left-most entries are ignored and the
// right-most untrusted entry (here 198.51.100.1) is the key.
func TestNewApp_ForgedLeftmostXFFFromTrustedProxy(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxies = []string{testPeerIP}
	app := newTestApp(t, cfg)

	codes := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		xff := "203.0.113." + strconv.Itoa(i) + ", 198.51.100.1"
		codes = append(codes, do(t, app, authRequest("wrong", xff)).StatusCode)
	}
	assert.Equal(t, []int{401, 401, 429, 429}, codes)
}

func TestNewApp_InvalidConfig(t *testing.T) {
	l := logger.Discard()
	ts := auth.NewTokenStore(l)
	require.NoError(t, ts.Add("good-token", "tenant-a"))

	tests := []struct {
		name string
		mod  func(*config.Config)
	}{
		{"invalid trusted proxy", func(c *config.Config) { c.TrustedProxies = []string{"10.0.0.0/33"} }},
		{"zero max", func(c *config.Config) { c.RateLimitMax = 0 }},
		{"negative max", func(c *config.Config) { c.RateLimitMax = -1 }},
		{"zero window", func(c *config.Config) { c.RateLimitWindow = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			tt.mod(&cfg)
			_, err := newApp(cfg, l, metrics.New("test"), ts)
			assert.Error(t, err)
		})
	}

	// Rate limit settings are not validated when the limiter is disabled.
	cfg := testConfig()
	cfg.RateLimitEnabled = false
	cfg.RateLimitMax = 0
	_, err := newApp(cfg, l, metrics.New("test"), ts)
	assert.NoError(t, err)
}

func TestNewApp_RateLimitDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitEnabled = false
	app := newTestApp(t, cfg)

	for _, code := range failedAttempts(t, app, 5) {
		assert.Equal(t, http.StatusUnauthorized, code)
	}
}

// Shutdown over a real listener: in-flight server stops and Listener returns.
func TestNewApp_GracefulShutdown(t *testing.T) {
	app := newTestApp(t, testConfig())
	base, errCh := startServer(t, app)

	require.NoError(t, app.ShutdownWithTimeout(5*time.Second))
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after shutdown")
	}

	_, err := testClient.Get(base + "/healthz")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "reset"), err.Error())
}
