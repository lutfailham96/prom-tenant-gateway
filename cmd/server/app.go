package main

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
)

// bodyLimit caps request bodies: all endpoints are GET-only and auth
// subrequests from the ingress carry no body.
const bodyLimit = 4 * 1024

// newApp builds the Fiber application with its production configuration and
// routes. It is separate from main so tests exercise the real wiring.
func newApp(cfg config.Config, log *slog.Logger, m *metrics.Metrics, tokenStore *auth.TokenStore) *fiber.App {
	h := handler.New(tokenStore, m, version)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log),
		// X-Forwarded-For is only honoured when the peer is a trusted proxy;
		// otherwise any caller could spoof its IP and bypass the rate limiter.
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          cfg.TrustedProxies,
		// Resolve c.IP() to a single valid IP instead of the raw header value.
		EnableIPValidation: true,
		BodyLimit:          bodyLimit,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		IdleTimeout:        120 * time.Second,
	})

	if len(cfg.TrustedProxies) == 0 {
		log.Warn("TRUSTED_PROXIES not set: X-Forwarded-For is ignored and rate limiting keys on the proxy IP")
	}

	// RequestObserver goes first so that panics recovered below are still
	// measured and logged as 500s.
	app.Use(middleware.RequestObserver(log, m))
	app.Use(recover.New())

	app.Get("/metrics", h.Metrics)
	app.Get("/healthz", h.Health)

	// Rate limiting only on auth endpoint
	if cfg.RateLimitEnabled {
		rateLimiter := middleware.RateLimiter(m, cfg.RateLimitMax, cfg.RateLimitWindow)
		app.Get("/validate-token", rateLimiter, h.ValidateToken)
		log.Info("Rate limiting enabled on /validate-token",
			slog.Int("max_failed_attempts", cfg.RateLimitMax),
			slog.String("window", cfg.RateLimitWindow.String()),
		)
	} else {
		app.Get("/validate-token", h.ValidateToken)
		log.Info("Rate limiting disabled")
	}

	return app
}
