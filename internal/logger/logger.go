// Package logger builds the service's structured JSON logger on top of
// log/slog. The output schema is kept compatible with the previous custom
// logger: timestamp (UTC, RFC3339Nano), level (lowercase), message, service,
// version, followed by call-site attributes.
package logger

import (
	"io"
	"log/slog"
	"strings"
	"time"
)

// ParseLevel maps a LOG_LEVEL value to a slog level. Unknown values default
// to info.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a JSON logger writing to w at the given level, with service
// and version attached to every record.
func New(w io.Writer, service, version string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(h).With(
		slog.String("service", service),
		slog.String("version", version),
	)
}

// Discard returns a logger that drops everything; intended for tests.
func Discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.String("timestamp", a.Value.Time().UTC().Format(time.RFC3339Nano))
	case slog.LevelKey:
		return slog.String("level", levelName(a.Value.Any()))
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

func levelName(v any) string {
	level, ok := v.(slog.Level)
	if !ok {
		return "info"
	}
	switch {
	case level < slog.LevelInfo:
		return "debug"
	case level < slog.LevelWarn:
		return "info"
	case level < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}
