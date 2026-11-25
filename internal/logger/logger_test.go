package logger_test

import (
	"bytes"
	"encoding/json"
	"log"
	"testing"

	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBufferLogger(level string) (*logger.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return &logger.Logger{Logger: log.New(&buf, "", 0), Service: "test", Version: "1.0.0", Level: level}, &buf
}

func TestLogger_JSONFormat(t *testing.T) {
	l, buf := newBufferLogger("info")

	l.Info("test message", map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	})

	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "info", entry["level"])
	assert.Equal(t, "test message", entry["message"])
	assert.Equal(t, "test", entry["service"])
	assert.Equal(t, "1.0.0", entry["version"])
	assert.Equal(t, "value1", entry["key1"])
	assert.Equal(t, float64(123), entry["key2"])
	assert.NotEmpty(t, entry["timestamp"])
}

func TestLogger_LevelFiltering(t *testing.T) {
	l, buf := newBufferLogger("info")

	l.Debug("dropped debug message", nil)
	assert.Zero(t, buf.Len())

	l.Info("allowed info message", nil)
	assert.Contains(t, buf.String(), "allowed info message")

	buf.Reset()
	l.SetLevel("debug")
	l.Debug("allowed debug message", nil)
	assert.Contains(t, buf.String(), "allowed debug message")

	buf.Reset()
	l.SetLevel("error")
	l.Warn("dropped warn message", nil)
	assert.Zero(t, buf.Len())
}

func TestLogger_New(t *testing.T) {
	l := logger.New("svc", "9.9.9")

	assert.Equal(t, "svc", l.Service)
	assert.Equal(t, "9.9.9", l.Version)
	assert.Equal(t, "info", l.Level)
}
