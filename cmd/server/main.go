package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/monitor"
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
	m := metrics.New()
	tokenStore := auth.NewTokenStore(log)

	tokenStore.LoadFromEnv()

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
	})

	app.Use(recover.New())
	app.Use(middleware.RequestLogger(log))

	app.Get("/metrics", h.Metrics)
	app.Get("/healthz", h.Health)
	app.Get("/dashboard", monitor.New())

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
		if err := app.Shutdown(); err != nil {
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