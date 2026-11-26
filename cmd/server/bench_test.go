package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/valyala/fasthttp"
)

// benchmarkValidate drives the production app (info-level JSON logging to a
// discarded writer) through the fasthttp handler, without network overhead.
func benchmarkValidate(b *testing.B, authorization string) {
	l := logger.New(io.Discard, serviceName, version, slog.LevelInfo)
	ts := auth.NewTokenStore(logger.Discard())
	if err := ts.Add("good-token", "tenant-a"); err != nil {
		b.Fatal(err)
	}
	m := metrics.New(version)
	m.SetTenants(ts.Tenants(), ts.Count())
	cfg := testConfig()
	cfg.RateLimitEnabled = false // the invalid-token benchmark would otherwise measure 429s
	app, err := newApp(cfg, l, m, ts)
	if err != nil {
		b.Fatal(err)
	}
	hnd := app.Handler()

	b.ReportAllocs()
	for b.Loop() {
		var ctx fasthttp.RequestCtx
		ctx.Request.SetRequestURI("/validate-token")
		ctx.Request.Header.SetMethod(fasthttp.MethodGet)
		ctx.Request.Header.Set(fasthttp.HeaderAuthorization, authorization)
		ctx.Request.Header.Set(fasthttp.HeaderUserAgent, "Prometheus/3.0")
		hnd(&ctx)
	}
}

func BenchmarkValidateToken_Success(b *testing.B) { benchmarkValidate(b, "Bearer good-token") }
func BenchmarkValidateToken_Invalid(b *testing.B) { benchmarkValidate(b, "Bearer wrong") }
