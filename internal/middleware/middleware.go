package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
)

func RequestLogger(log *logger.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		latency := time.Since(start)
		log.Info("HTTP request", map[string]interface{}{
			"method":      c.Method(),
			"path":        c.Path(),
			"status":      c.Response().StatusCode(),
			"latency":     latency.String(),
			"latency_ms":  latency.Milliseconds(),
			"ip":          c.IP(),
			"user_agent":  c.Get("User-Agent"),
		})
		return err
	}
}

func RateLimiter(log *logger.Logger, m *metrics.Metrics, max int, window time.Duration) fiber.Handler {
	if max == 0 {
		max = 100
	}
	if window == 0 {
		window = 1 * time.Minute
	}

	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: window,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			m.IncFailed()
			log.Warn("Rate limit exceeded", map[string]interface{}{
				"ip":     c.IP(),
				"path":   c.Path(),
				"method": c.Method(),
			})
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "rate limit exceeded",
			})
		},
	})
}