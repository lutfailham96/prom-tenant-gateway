package handler

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
)

type Handler struct {
	tokenStore *auth.TokenStore
	logger     *logger.Logger
	metrics    *metrics.Metrics
}

func New(tokenStore *auth.TokenStore, log *logger.Logger, m *metrics.Metrics) *Handler {
	return &Handler{
		tokenStore: tokenStore,
		logger:     log,
		metrics:    m,
	}
}

func (h *Handler) ValidateToken(c *fiber.Ctx) error {
	h.metrics.IncTotal()

	authHeader := c.Get("Authorization")
	if authHeader == "" {
		h.metrics.IncFailed()
		h.logger.Warn("Missing Authorization header", map[string]interface{}{
			"ip":     c.IP(),
			"path":   c.Path(),
			"method": c.Method(),
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing Authorization header"})
	}

	token, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok {
		h.metrics.IncFailed()
		h.logger.Warn("Invalid Authorization header format", map[string]interface{}{
			"ip":     c.IP(),
			"path":   c.Path(),
			"method": c.Method(),
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid Authorization header"})
	}

	tenantID, found := h.tokenStore.Validate(token)
	if !found {
		h.metrics.IncFailed()
		h.logger.Warn("Invalid token attempt", map[string]interface{}{
			"ip":     c.IP(),
			"path":   c.Path(),
			"method": c.Method(),
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
	}

	h.metrics.IncSuccess()
	c.Set("X-Scope-OrgID", tenantID)
	h.logger.Info("Authenticated tenant", map[string]interface{}{
		"tenant_id": tenantID,
		"ip":        c.IP(),
		"path":      c.Path(),
		"method":    c.Method(),
	})

	return c.SendStatus(fiber.StatusOK)
}

func (h *Handler) Health(c *fiber.Ctx) error {
	_, _, _, uptimeSec := h.metrics.Get()
	return c.JSON(fiber.Map{
		"status":    "healthy",
		"uptime":    strconv.FormatFloat(uptimeSec, 'f', 2, 64) + "s",
		"timestamp": c.Context().Time().UTC().Format("2006-01-02T15:04:05Z"),
		"version":   "1.0.0",
	})
}

func (h *Handler) Metrics(c *fiber.Ctx) error {
	total, success, failed, uptimeSec := h.metrics.Get()
	tokenCount := h.tokenStore.Count()

	metricsOutput := `# HELP go_grafana_auth_requests_total Total number of requests
# TYPE go_grafana_auth_requests_total counter
go_grafana_auth_requests_total ` + strconv.FormatUint(total, 10) + `
# HELP go_grafana_auth_requests_success_total Total number of successful requests
# TYPE go_grafana_auth_requests_success_total counter
go_grafana_auth_requests_success_total ` + strconv.FormatUint(success, 10) + `
# HELP go_grafana_auth_requests_failed_total Total number of failed requests
# TYPE go_grafana_auth_requests_failed_total counter
go_grafana_auth_requests_failed_total ` + strconv.FormatUint(failed, 10) + `
# HELP go_grafana_auth_uptime_seconds Service uptime in seconds
# TYPE go_grafana_auth_uptime_seconds gauge
go_grafana_auth_uptime_seconds ` + strconv.FormatFloat(uptimeSec, 'f', 2, 64) + `
# HELP go_grafana_auth_tokens_configured Number of configured valid tokens
# TYPE go_grafana_auth_tokens_configured gauge
go_grafana_auth_tokens_configured ` + strconv.Itoa(tokenCount) + `
`

	c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	return c.SendString(metricsOutput)
}

func CustomErrorHandler(log *logger.Logger, m *metrics.Metrics) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		m.IncFailed()
		code := fiber.StatusInternalServerError
		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}
		log.Error("Request error", map[string]interface{}{
			"path":   c.Path(),
			"method": c.Method(),
			"ip":     c.IP(),
			"error":  err.Error(),
			"status": code,
		})
		return c.Status(code).JSON(fiber.Map{"error": err.Error()})
	}
}