package middleware

import (
	"log/slog"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/handler"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
)

// RequestObserver records latency metrics for every request and writes one
// log line per request (except health/metrics probes).
//
// Errors returned by later handlers are passed to the app's error handler
// here, so the logged and measured status code is the one actually sent
// (otherwise Fiber would run the error handler after this middleware returns
// and every error would be recorded as 200).
//
// Log level follows the status: <400 debug (successful auth traffic is
// high-volume), 401/403/429 warn (security-relevant), other 4xx info,
// 5xx error. Register it before the recover middleware so panics are
// observed as 500s.
//
// clientIP resolves the logged client IP (see clientip.Resolver.IP); nil
// uses the TCP peer address.
func RequestObserver(log *slog.Logger, m *metrics.Metrics, clientIP func(*fiber.Ctx) string) fiber.Handler {
	if clientIP == nil {
		clientIP = func(c *fiber.Ctx) string { return c.Context().RemoteIP().String() }
	}
	var routes routeSet
	return func(c *fiber.Ctx) error {
		start := time.Now()

		if chainErr := c.Next(); chainErr != nil {
			if err := c.App().ErrorHandler(c, chainErr); err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		latency := time.Since(start)
		status := c.Response().StatusCode()
		m.ObserveHTTP(routes.label(c), normalizeMethod(c.Method()), status, latency)

		path := c.Path()
		if path == "/healthz" || path == "/metrics" {
			return nil
		}

		ctx := c.UserContext()
		level := levelForStatus(status)
		if !log.Enabled(ctx, level) {
			return nil
		}

		attrs := make([]slog.Attr, 0, 9)
		attrs = append(attrs,
			slog.String("method", c.Method()),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Float64("latency_ms", float64(latency.Microseconds())/1000),
			slog.String("ip", clientIP(c)),
			slog.String("user_agent", c.Get(fiber.HeaderUserAgent)),
		)
		if result, ok := c.Locals(metrics.LocalsAuthResult).(string); ok {
			attrs = append(attrs, slog.String("auth_result", result))
		}
		if tenant := c.GetRespHeader(handler.HeaderScopeOrgID); tenant != "" {
			attrs = append(attrs, slog.String("tenant_id", tenant))
		}
		log.LogAttrs(ctx, level, "HTTP request", attrs...)
		return nil
	}
}

func levelForStatus(status int) slog.Level {
	switch {
	case status >= fiber.StatusInternalServerError:
		return slog.LevelError
	case status == fiber.StatusUnauthorized, status == fiber.StatusForbidden, status == fiber.StatusTooManyRequests:
		return slog.LevelWarn
	case status >= fiber.StatusBadRequest:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

type routeKey struct{ method, path string }

// routeSet holds the app's registered (non-middleware) routes. It is built
// lazily on the first request, when all routes have been registered.
type routeSet struct {
	once   sync.Once
	routes map[routeKey]struct{}
}

// label returns the matched route pattern, or metrics.RouteUnmatched when no
// registered route matched (e.g. 404s, where c.Route() is the app.Use
// middleware route). This keeps the label set bounded.
func (s *routeSet) label(c *fiber.Ctx) string {
	s.once.Do(func() {
		s.routes = map[routeKey]struct{}{}
		for _, r := range c.App().GetRoutes(true) { // true: exclude app.Use routes
			s.routes[routeKey{r.Method, r.Path}] = struct{}{}
		}
	})
	r := c.Route()
	if r == nil {
		return metrics.RouteUnmatched
	}
	if _, ok := s.routes[routeKey{r.Method, r.Path}]; !ok {
		return metrics.RouteUnmatched
	}
	return r.Path
}

// normalizeMethod maps the request method to a constant string: the raw value
// may reference a reused request buffer and is attacker-controlled.
func normalizeMethod(method string) string {
	switch method {
	case fiber.MethodGet:
		return fiber.MethodGet
	case fiber.MethodHead:
		return fiber.MethodHead
	case fiber.MethodPost:
		return fiber.MethodPost
	case fiber.MethodPut:
		return fiber.MethodPut
	case fiber.MethodDelete:
		return fiber.MethodDelete
	case fiber.MethodPatch:
		return fiber.MethodPatch
	case fiber.MethodOptions:
		return fiber.MethodOptions
	default:
		return "other"
	}
}
