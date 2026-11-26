package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/clientip"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
	"github.com/lutfailham96/prom-tenant-gateway/internal/ratelimit"
)

// bodyLimit caps request bodies: all endpoints are GET-only and auth
// subrequests from the ingress carry no body.
const bodyLimit = 4 * 1024

// newApp builds the Fiber application with its production configuration and
// routes. It is separate from main so tests exercise the real wiring. It
// fails on invalid configuration (TRUSTED_PROXIES, rate limit settings).
func newApp(cfg config.Config, log *slog.Logger, m *metrics.Metrics, tokenStore *auth.TokenStore) (*fiber.App, error) {
	// Client IP resolution is done by clientip.Resolver instead of Fiber's
	// ProxyHeader/TrustedProxies: Fiber takes the left-most X-Forwarded-For
	// entry, which a client can forge whenever a trusted proxy appends to
	// the header. The resolver uses the right-most untrusted entry and
	// ignores the header entirely unless the TCP peer is a trusted proxy.
	ips, err := clientip.New(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	if !ips.HasTrustedProxies() {
		log.Warn("TRUSTED_PROXIES not set: X-Forwarded-For is ignored and rate limiting keys on the proxy IP")
	}

	var opts []handler.Option
	if cfg.RateLimitEnabled {
		limiter, err := ratelimit.New(cfg.RateLimitMax, cfg.RateLimitWindow)
		if err != nil {
			return nil, fmt.Errorf("rate limit (RATE_LIMIT_MAX=%d, RATE_LIMIT_WINDOW=%s): %w",
				cfg.RateLimitMax, cfg.RateLimitWindow, err)
		}
		opts = append(opts, handler.WithFailureLimiter(limiter, ips.IP))
		log.Info("Rate limiting enabled on /validate-token",
			slog.Int("max_failed_attempts", limiter.Max()),
			slog.String("window", limiter.Window().String()),
		)
	} else {
		log.Info("Rate limiting disabled")
	}
	h := handler.New(tokenStore, m, version, opts...)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log),
		BodyLimit:             bodyLimit,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           120 * time.Second,
	})

	// RequestObserver goes first so that panics recovered below are still
	// measured and logged as 500s.
	app.Use(middleware.RequestObserver(log, m, ips.IP))
	app.Use(recover.New())

	app.Get("/metrics", h.Metrics)
	app.Get("/healthz", h.Health)
	app.Get("/validate-token", h.ValidateToken)

	return app, nil
}
