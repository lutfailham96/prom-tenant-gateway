package logger

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

type Logger struct {
	*log.Logger
	Service string
	Version string
}

func New(service, version string) *Logger {
	return &Logger{
		Logger:  log.New(os.Stdout, "", 0),
		Service: service,
		Version: version,
	}
}

func (l *Logger) log(level, message string, fields map[string]interface{}) {
	entry := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"message":   message,
		"service":   l.Service,
		"version":   l.Version,
	}
	for k, v := range fields {
		entry[k] = v
	}
	data, _ := json.Marshal(entry)
	l.Println(string(data))
}

func (l *Logger) Info(message string, fields map[string]interface{}) {
	l.log("info", message, fields)
}

func (l *Logger) Warn(message string, fields map[string]interface{}) {
	l.log("warn", message, fields)
}

func (l *Logger) Error(message string, fields map[string]interface{}) {
	l.log("error", message, fields)
}

func (l *Logger) Debug(message string, fields map[string]interface{}) {
	l.log("debug", message, fields)
}