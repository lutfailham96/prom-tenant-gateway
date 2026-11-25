package middleware

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeMethod(t *testing.T) {
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		assert.Equal(t, m, normalizeMethod(m))
	}
	for _, m := range []string{"BREW", "get", "", "PROPFIND"} {
		assert.Equal(t, "other", normalizeMethod(m), "%q", m)
	}
}

func TestLevelForStatus(t *testing.T) {
	tests := map[int]slog.Level{
		200: slog.LevelDebug,
		204: slog.LevelDebug,
		301: slog.LevelDebug,
		400: slog.LevelInfo,
		401: slog.LevelWarn,
		403: slog.LevelWarn,
		404: slog.LevelInfo,
		413: slog.LevelInfo,
		429: slog.LevelWarn,
		500: slog.LevelError,
		503: slog.LevelError,
	}
	for status, want := range tests {
		assert.Equal(t, want, levelForStatus(status), "%d", status)
	}
}
