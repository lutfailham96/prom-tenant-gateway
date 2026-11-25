package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/lutfailham96/prom-tenant-gateway/internal/middleware"
)

const (
	serviceName = "prom-tenant-gateway"
	version     = "1.0.0"
)

func main() {
	log := logger.New(serviceName, version)
	cfg := config.Load()
	log.SetLevel(cfg.LogLevel)
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	if err := tokenStore.LoadFromEnv(); err != nil {
		log.Error("Invalid token configuration", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	if tokenStore.Count() == 0 {
		log.Error("No valid tokens configured", map[string]interface{}{
			"error": "Set TENANT_*_TOKEN or VALID_TOKENS_JSON environment variables",
		})
		os.Exit(1)
	}

	h := handler.New(tokenStore, log, m)

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          handler.CustomErrorHandler(log, m),
		// X-Forwarded-For is only honoured when the peer is a trusted proxy;
		// otherwise any caller could spoof its IP and bypass the rate limiter.
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          cfg.TrustedProxies,
		// Resolve c.IP() to a single valid IP instead of the raw header value.
		EnableIPValidation: true,
		// All endpoints are GET-only; auth subrequests carry no body.
		BodyLimit:    4 * 1024,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	})

	if len(cfg.TrustedProxies) == 0 {
		log.Warn("TRUSTED_PROXIES not set: X-Forwarded-For is ignored and rate limiting keys on the proxy IP", map[string]interface{}{})
	}

	app.Use(recover.New())
	app.Use(middleware.RequestLogger(log))

	app.Get("/metrics", h.Metrics)
	app.Get("/healthz", h.Health)

	// Rate limiting only on auth endpoint
	if cfg.RateLimitEnabled {
		rateLimiter := middleware.RateLimiter(log, m, cfg.RateLimitMax, cfg.RateLimitWindow)
		app.Get("/validate-token", rateLimiter, h.ValidateToken)
		log.Info("Rate limiting enabled on /validate-token", map[string]interface{}{
			"max_requests": cfg.RateLimitMax,
			"window":       cfg.RateLimitWindow.String(),
		})
	} else {
		app.Get("/validate-token", h.ValidateToken)
		log.Info("Rate limiting disabled", map[string]interface{}{})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Info("Shutdown signal received, stopping server...", map[string]interface{}{})
		cancel()
	}()

	addr := ":" + cfg.Port
	log.Info("Starting authentication service", map[string]interface{}{
		"address": addr,
		"port":    cfg.Port,
	})

	errCh := make(chan error, 1)
	go func() {
		if err := app.Listen(addr); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			log.Error("Error during shutdown", map[string]interface{}{
				"error": err.Error(),
			})
			os.Exit(1)
		}
		log.Info("Shutdown complete", map[string]interface{}{})
	case err := <-errCh:
		log.Error("Server failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
}
