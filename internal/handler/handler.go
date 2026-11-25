package handler

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
)

// HeaderScopeOrgID carries the authenticated tenant to Mimir.
const HeaderScopeOrgID = "X-Scope-OrgID"

// Pre-encoded error bodies: the 401 path is hit by every failed attempt, so
// avoid JSON encoding per request.
var (
	bodyMissingHeader = []byte(`{"error":"missing Authorization header"}`)
	bodyInvalidHeader = []byte(`{"error":"invalid Authorization header"}`)
	bodyInvalidToken  = []byte(`{"error":"invalid token"}`)
)

type Handler struct {
	tokenStore     *auth.TokenStore
	metrics        *metrics.Metrics
	metricsHandler fiber.Handler
	version        string
}

// New returns the HTTP handlers; version is reported by /healthz.
func New(tokenStore *auth.TokenStore, m *metrics.Metrics, version string) *Handler {
	return &Handler{
		tokenStore:     tokenStore,
		metrics:        m,
		metricsHandler: adaptor.HTTPHandler(m.Handler()),
		version:        version,
	}
}

// ValidateToken authenticates "Authorization: Bearer <token>" and, on
// success, returns the tenant in X-Scope-OrgID. It does not log: the request
// observer middleware writes one log line per request and reads the result
// from c.Locals(metrics.LocalsAuthResult).
func (h *Handler) ValidateToken(c *fiber.Ctx) error {
	authHeader := c.Get(fiber.HeaderAuthorization)
	if authHeader == "" {
		h.metrics.AuthFailure(metrics.ResultMissingHeader)
		c.Locals(metrics.LocalsAuthResult, metrics.ResultMissingHeader)
		return unauthorized(c, bodyMissingHeader)
	}

	token, ok := bearerToken(authHeader)
	if !ok {
		h.metrics.AuthFailure(metrics.ResultInvalidHeader)
		c.Locals(metrics.LocalsAuthResult, metrics.ResultInvalidHeader)
		return unauthorized(c, bodyInvalidHeader)
	}

	tenantID, found := h.tokenStore.Validate(token)
	if !found {
		h.metrics.AuthFailure(metrics.ResultInvalidToken)
		c.Locals(metrics.LocalsAuthResult, metrics.ResultInvalidToken)
		return unauthorized(c, bodyInvalidToken)
	}

	h.metrics.AuthSuccess(tenantID)
	c.Locals(metrics.LocalsAuthResult, metrics.ResultSuccess)
	c.Set(HeaderScopeOrgID, tenantID)
	return c.SendStatus(fiber.StatusOK)
}

func unauthorized(c *fiber.Ctx, body []byte) error {
	c.Status(fiber.StatusUnauthorized)
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Send(body)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. The scheme is matched case-insensitively without allocating.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

func (h *Handler) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":    "healthy",
		"uptime":    strconv.FormatFloat(h.metrics.Uptime().Seconds(), 'f', 2, 64) + "s",
		"timestamp": c.Context().Time().UTC().Format("2006-01-02T15:04:05Z"),
		"version":   h.version,
	})
}

// Metrics serves Prometheus metrics (gateway, Go runtime and process).
func (h *Handler) Metrics(c *fiber.Ctx) error {
	return h.metricsHandler(c)
}

// CustomErrorHandler turns errors into JSON responses. Only *fiber.Error
// messages are shown to clients; anything else may carry internal details and
// is replaced with a generic message. 5xx errors are logged with their cause;
// other errors are already covered by the request log line.
func CustomErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		message := "internal server error"
		var fe *fiber.Error
		if errors.As(err, &fe) {
			code = fe.Code
			message = fe.Message
		}

		level := slog.LevelDebug
		if code >= fiber.StatusInternalServerError {
			level = slog.LevelError
		}
		log.LogAttrs(c.UserContext(), level, "Request error",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", code),
			slog.String("error", err.Error()),
		)
		return c.Status(code).JSON(fiber.Map{"error": message})
	}
}
