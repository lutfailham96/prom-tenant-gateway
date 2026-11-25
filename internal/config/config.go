package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port             string
	RateLimitEnabled bool
	RateLimitMax     int
	RateLimitWindow  time.Duration
	LogLevel         string
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
		Port:             port,
		RateLimitEnabled: rateLimitEnabled,
		RateLimitMax:     rateLimitMax,
		RateLimitWindow:  rateLimitWindow,
		LogLevel:         logLevel,
	}
}
