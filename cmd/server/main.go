package main

import (
	"context"
	"log/slog"
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
	cfg := config.Load()
	log := logger.New(os.Stdout, serviceName, version, logger.ParseLevel(cfg.LogLevel))
	m := metrics.New(version)
	tokenStore := auth.NewTokenStore(log)

	if err := tokenStore.LoadFromEnv(); err != nil {
		log.Error("Invalid token configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if tokenStore.Count() == 0 {
		log.Error("No valid tokens configured",
			slog.String("error", "Set TENANT_*_TOKEN or VALID_TOKENS_JSON environment variables"))
		os.Exit(1)
	}
	m.SetTenants(tokenStore.Tenants(), tokenStore.Count())

	app := newApp(cfg, log, m, tokenStore)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	addr := ":" + cfg.Port
	log.Info("Starting authentication service", slog.String("address", addr), slog.String("port", cfg.Port))

	errCh := make(chan error, 1)
	go func() {
		if err := app.Listen(addr); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("Shutdown signal received, stopping server...")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			log.Error("Error during shutdown", slog.String("error", err.Error()))
			os.Exit(1)
		}
		log.Info("Shutdown complete")
	case err := <-errCh:
		log.Error("Server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
