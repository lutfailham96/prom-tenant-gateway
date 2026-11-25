package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
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

	app := newApp(cfg, log, m, tokenStore)

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
