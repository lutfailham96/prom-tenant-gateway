package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/config"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
)

const serviceName = "prom-tenant-gateway"

// version is set at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/server
//
// The Dockerfile passes it from the VERSION build arg, which CI derives from
// the git tag. Without ldflags it falls back to the module version recorded by
// the Go toolchain, or "dev".
var version = "dev"

func init() {
	version = resolveVersion(version)
}

// resolveVersion returns v unless it is the "dev" placeholder, in which case
// it tries the main module version from the embedded build info.
func resolveVersion(v string) string {
	if v != "dev" {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return v
}

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

	app, err := newApp(cfg, log, m, tokenStore)
	if err != nil {
		log.Error("Invalid configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

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
