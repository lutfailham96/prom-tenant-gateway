// Package metrics exposes the gateway's Prometheus metrics on a dedicated
// registry (no global state), together with Go runtime and process metrics.
package metrics

import (
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "prom_tenant_gateway"

// Auth request results, used as the "result" label.
const (
	ResultSuccess       = "success"
	ResultMissingHeader = "missing_header"
	ResultInvalidHeader = "invalid_header"
	ResultInvalidToken  = "invalid_token"
	ResultRateLimited   = "rate_limited"
)

// LocalsAuthResult is the fiber.Ctx Locals key under which the auth result
// (one of the Result* constants) is stored for the request log.
const LocalsAuthResult = "auth_result"

var failureResults = []string{ResultMissingHeader, ResultInvalidHeader, ResultInvalidToken, ResultRateLimited}

// RouteUnmatched is the route label for requests that matched no route.
const RouteUnmatched = "unmatched"

type httpKey struct {
	route, method string
	code          int
}

type Metrics struct {
	registry *prometheus.Registry
	start    time.Time

	authRequests *prometheus.CounterVec
	// Pre-resolved children keep the hot path free of label allocations.
	authFailures map[string]prometheus.Counter
	authSuccess  map[string]prometheus.Counter // by tenant; written only by SetTenants

	httpDuration *prometheus.HistogramVec
	httpMu       sync.RWMutex
	httpObs      map[httpKey]prometheus.Observer

	tokensConfigured prometheus.Gauge
}

// New creates the metrics and registers them, plus Go runtime and process
// collectors, on a fresh registry.
func New(version string) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		start:    time.Now(),
		authRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "auth_requests_total",
			Help:      "Token validation requests by result; tenant is set only for successful requests.",
		}, []string{"result", "tenant"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency by route, method and status code.",
			// The gateway answers from memory: most requests take well under 1ms.
			Buckets: []float64{.0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, 1},
		}, []string{"route", "method", "code"}),
		tokensConfigured: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "tokens_configured",
			Help:      "Number of configured tenant tokens.",
		}),
		authFailures: make(map[string]prometheus.Counter, len(failureResults)),
		authSuccess:  map[string]prometheus.Counter{},
		httpObs:      map[httpKey]prometheus.Observer{},
	}
	for _, result := range failureResults {
		m.authFailures[result] = m.authRequests.WithLabelValues(result, "")
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build information; the value is always 1.",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	m.registry.MustRegister(
		m.authRequests,
		m.httpDuration,
		m.tokensConfigured,
		buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// SetTenants pre-creates the success counter for each tenant and records the
// number of configured tokens. Call it once at startup, before serving.
func (m *Metrics) SetTenants(tenants []string, tokenCount int) {
	for _, tenant := range tenants {
		m.authSuccess[tenant] = m.authRequests.WithLabelValues(ResultSuccess, tenant)
	}
	m.tokensConfigured.Set(float64(tokenCount))
}

// AuthSuccess counts a successful validation for tenant.
func (m *Metrics) AuthSuccess(tenant string) {
	if c, ok := m.authSuccess[tenant]; ok {
		c.Inc()
		return
	}
	m.authRequests.WithLabelValues(ResultSuccess, tenant).Inc()
}

// AuthFailure counts a failed validation; result is one of the Result*
// failure constants.
func (m *Metrics) AuthFailure(result string) {
	if c, ok := m.authFailures[result]; ok {
		c.Inc()
		return
	}
	m.authRequests.WithLabelValues(result, "").Inc()
}

// ObserveHTTP records a request's latency. route must be a registered route
// pattern (or RouteUnmatched) and method a normalized method, so that label
// values come from a bounded set and never reference request buffers.
func (m *Metrics) ObserveHTTP(route, method string, code int, d time.Duration) {
	key := httpKey{route: route, method: method, code: code}

	m.httpMu.RLock()
	obs, ok := m.httpObs[key]
	m.httpMu.RUnlock()

	if !ok {
		obs = m.httpDuration.WithLabelValues(route, method, strconv.Itoa(code))
		m.httpMu.Lock()
		m.httpObs[key] = obs
		m.httpMu.Unlock()
	}
	obs.Observe(d.Seconds())
}

// Uptime returns the time since the metrics were created (process start).
func (m *Metrics) Uptime() time.Duration {
	return time.Since(m.start)
}

// Handler serves the registry in the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Registry exposes the underlying registry, mainly for tests.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
