package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port             string
	RateLimitEnabled bool
	RateLimitMax     int
	RateLimitWindow  time.Duration
	LogLevel         string
	// TrustedProxies lists proxy IPs/CIDRs whose X-Forwarded-For header is
	// honoured. When empty, X-Forwarded-For is ignored and the TCP peer
	// address is used as the client IP.
	TrustedProxies []string
}

func Load() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = os.Getenv("HTTP_PORT")
	}
	if port == "" {
		port = "8080"
	}

	rateLimitEnabled := true
	if v := os.Getenv("RATE_LIMIT_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			rateLimitEnabled = parsed
		}
	}

	rateLimitMax := 100
	if v := os.Getenv("RATE_LIMIT_MAX"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			rateLimitMax = parsed
		}
	}

	rateLimitWindow := 1 * time.Minute
	if v := os.Getenv("RATE_LIMIT_WINDOW"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			rateLimitWindow = parsed
		}
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	return Config{
		TrustedProxies:   splitList(os.Getenv("TRUSTED_PROXIES")),
		Port:             port,
		RateLimitEnabled: rateLimitEnabled,
		RateLimitMax:     rateLimitMax,
		RateLimitWindow:  rateLimitWindow,
		LogLevel:         logLevel,
	}
}

// splitList parses a comma-separated list, dropping empty entries.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
