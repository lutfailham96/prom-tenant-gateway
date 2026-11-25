package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		entries = append(entries, entry)
	}
	return entries
}

// The output schema stays compatible with the previous custom logger.
func TestNew_JSONSchema(t *testing.T) {
	var buf bytes.Buffer
	l := logger.New(&buf, "test", "1.0.0", slog.LevelInfo)

	l.Info("test message", slog.String("key1", "value1"), slog.Int("key2", 123))

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 1)
	entry := entries[0]
	assert.Equal(t, "info", entry["level"])
	assert.Equal(t, "test message", entry["message"])
	assert.Equal(t, "test", entry["service"])
	assert.Equal(t, "1.0.0", entry["version"])
	assert.Equal(t, "value1", entry["key1"])
	assert.Equal(t, float64(123), entry["key2"])
	assert.NotContains(t, entry, "msg")
	assert.NotContains(t, entry, "time")

	ts, ok := entry["timestamp"].(string)
	require.True(t, ok)
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, parsed.Location())
	assert.True(t, strings.HasSuffix(ts, "Z"))
}

func TestNew_LevelNames(t *testing.T) {
	var buf bytes.Buffer
	l := logger.New(&buf, "test", "1.0.0", slog.LevelDebug)

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	var levels []string
	for _, entry := range decodeLines(t, &buf) {
		levels = append(levels, entry["level"].(string))
	}
	assert.Equal(t, []string{"debug", "info", "warn", "error"}, levels)
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := logger.New(&buf, "test", "1.0.0", slog.LevelInfo)

	l.Debug("dropped debug message")
	assert.Zero(t, buf.Len())

	l.Info("allowed info message")
	assert.Contains(t, buf.String(), "allowed info message")
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		" error ": slog.LevelError,
		"":        slog.LevelInfo,
		"verbose": slog.LevelInfo,
	}
	for in, want := range tests {
		assert.Equal(t, want, logger.ParseLevel(in), "%q", in)
	}
}

func TestDiscard(t *testing.T) {
	l := logger.Discard()
	assert.False(t, l.Enabled(t.Context(), slog.LevelError))
}
