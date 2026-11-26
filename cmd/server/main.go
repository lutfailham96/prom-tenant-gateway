package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
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

// shutdownTimeout bounds the graceful shutdown; it must stay below the pod's
// terminationGracePeriodSeconds minus the preStop sleep.
const shutdownTimeout = 10 * time.Second

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
	return resolveVersionFrom(v, debug.ReadBuildInfo)
}

func resolveVersionFrom(v string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if v != "dev" {
		return v
	}
	if bi, ok := readBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return v
}

func main() {
	ctx, stop := notifyShutdown()
	code := run(ctx, runOptions{
		stdout:          os.Stdout,
		listen:          net.Listen,
		shutdownTimeout: shutdownTimeout,
	})
	stop() // explicit: os.Exit skips deferred calls
	os.Exit(code)
}

// notifyShutdown returns a context that is cancelled on SIGTERM or SIGINT.
func notifyShutdown() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

// runOptions holds the process-level dependencies of run, so tests can
// capture the output, the listener and use a short shutdown timeout.
type runOptions struct {
	stdout          io.Writer
	listen          func(network, address string) (net.Listener, error)
	shutdownTimeout time.Duration
}

// run loads the configuration from the environment, serves until ctx is
// cancelled and then shuts down gracefully. It returns the process exit
// code: 0 after a clean shutdown, 1 on invalid configuration, a server
// failure or a shutdown that exceeds opts.shutdownTimeout.
func run(ctx context.Context, opts runOptions) int {
	cfg := config.Load()
	log := logger.New(opts.stdout, serviceName, version, logger.ParseLevel(cfg.LogLevel))
	m := metrics.New(version)
	tokenStore := auth.NewTokenStore(log)

	if err := tokenStore.LoadFromEnv(); err != nil {
		log.Error("Invalid token configuration", slog.String("error", err.Error()))
		return 1
	}

	if tokenStore.Count() == 0 {
		log.Error("No valid tokens configured",
			slog.String("error", "Set TENANT_*_TOKEN or VALID_TOKENS_JSON environment variables"))
		return 1
	}
	m.SetTenants(tokenStore.Tenants(), tokenStore.Count())

	app, err := newApp(cfg, log, m, tokenStore)
	if err != nil {
		log.Error("Invalid configuration", slog.String("error", err.Error()))
		return 1
	}

	addr := ":" + cfg.Port
	// Same network as app.Listen (Fiber's default is tcp4).
	ln, err := opts.listen(app.Config().Network, addr)
	if err != nil {
		log.Error("Server failed", slog.String("error", err.Error()))
		return 1
	}
	log.Info("Starting authentication service", slog.String("address", addr), slog.String("port", cfg.Port))

	// Listener returns nil both after Shutdown and when the listener is
	// closed underneath it (fasthttp maps "use of closed network connection"
	// to a clean stop), so any return before ctx is done is a failure.
	errCh := make(chan error, 1)
	go func() { errCh <- app.Listener(ln) }()

	select {
	case <-ctx.Done():
		log.Info("Shutdown signal received, stopping server...")
		if err := app.ShutdownWithTimeout(opts.shutdownTimeout); err != nil {
			log.Error("Error during shutdown", slog.String("error", err.Error()))
			return 1
		}
		log.Info("Shutdown complete")
		return 0
	case err := <-errCh:
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		log.Error("Server failed", slog.String("error", err.Error()))
		return 1
	}
}
