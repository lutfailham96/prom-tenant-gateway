package metrics_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthCounters(t *testing.T) {
	m := metrics.New("test")
	m.SetTenants([]string{"a", "b"}, 3)

	m.AuthSuccess("a")
	m.AuthSuccess("a")
	m.AuthSuccess("unregistered") // falls back to a dynamic label
	m.AuthFailure(metrics.ResultInvalidToken)
	m.AuthFailure(metrics.ResultRateLimited)

	expected := `
# HELP prom_tenant_gateway_auth_requests_total Token validation requests by result; tenant is set only for successful requests.
# TYPE prom_tenant_gateway_auth_requests_total counter
prom_tenant_gateway_auth_requests_total{result="invalid_header",tenant=""} 0
prom_tenant_gateway_auth_requests_total{result="invalid_token",tenant=""} 1
prom_tenant_gateway_auth_requests_total{result="missing_header",tenant=""} 0
prom_tenant_gateway_auth_requests_total{result="rate_limited",tenant=""} 1
prom_tenant_gateway_auth_requests_total{result="success",tenant="a"} 2
prom_tenant_gateway_auth_requests_total{result="success",tenant="b"} 0
prom_tenant_gateway_auth_requests_total{result="success",tenant="unregistered"} 1
# HELP prom_tenant_gateway_tokens_configured Number of configured tenant tokens.
# TYPE prom_tenant_gateway_tokens_configured gauge
prom_tenant_gateway_tokens_configured 3
`
	require.NoError(t, testutil.GatherAndCompare(m.Registry(), strings.NewReader(expected),
		"prom_tenant_gateway_auth_requests_total", "prom_tenant_gateway_tokens_configured"))
}

func TestObserveHTTP(t *testing.T) {
	m := metrics.New("test")

	m.ObserveHTTP("/validate-token", "GET", 200, 300*time.Microsecond)
	m.ObserveHTTP("/validate-token", "GET", 200, 2*time.Millisecond)
	m.ObserveHTTP(metrics.RouteUnmatched, "GET", 404, time.Microsecond)

	count, err := testutil.GatherAndCount(m.Registry(), "prom_tenant_gateway_http_request_duration_seconds")
	require.NoError(t, err)
	assert.Equal(t, 2, count, "one series per route/method/code")

	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != "prom_tenant_gateway_http_request_duration_seconds" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["route"] == "/validate-token" {
				assert.Equal(t, "200", labels["code"])
				assert.Equal(t, uint64(2), metric.GetHistogram().GetSampleCount())
				assert.InDelta(t, 0.0023, metric.GetHistogram().GetSampleSum(), 1e-9)
			}
		}
	}
}

func TestHandler_ExposesRuntimeAndBuildInfo(t *testing.T) {
	m := metrics.New("9.9.9")

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, `prom_tenant_gateway_build_info{goversion="`)
	assert.Contains(t, out, `version="9.9.9"} 1`)
	assert.Contains(t, out, "go_goroutines")
	assert.Contains(t, out, "process_start_time_seconds")
	assert.NotContains(t, out, "go_grafana_auth_")
}

func TestUptime(t *testing.T) {
	m := metrics.New("test")
	assert.GreaterOrEqual(t, m.Uptime(), time.Duration(0))
}

// The success and failure paths must not allocate per request.
func TestAuthCounters_ZeroAlloc(t *testing.T) {
	m := metrics.New("test")
	m.SetTenants([]string{"a"}, 1)

	assert.Zero(t, testing.AllocsPerRun(1000, func() { m.AuthSuccess("a") }))
	assert.Zero(t, testing.AllocsPerRun(1000, func() { m.AuthFailure(metrics.ResultInvalidToken) }))
	m.ObserveHTTP("/r", "GET", 200, time.Millisecond) // warm the cache
	assert.Zero(t, testing.AllocsPerRun(1000, func() { m.ObserveHTTP("/r", "GET", 200, time.Millisecond) }))
}
