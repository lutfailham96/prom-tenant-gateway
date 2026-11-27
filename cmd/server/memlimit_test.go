package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryLimit(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		limit   int64
		ok      bool
		wantErr bool
	}{
		{name: "unset", env: nil},
		{name: "90% of the container limit", env: map[string]string{"MEMORY_LIMIT_BYTES": "268435456"}, limit: 241591910, ok: true},
		{name: "explicit GOMEMLIMIT wins", env: map[string]string{"GOMEMLIMIT": "100MiB", "MEMORY_LIMIT_BYTES": "268435456"}},
		{name: "unit suffix", env: map[string]string{"MEMORY_LIMIT_BYTES": "256Mi"}, wantErr: true},
		{name: "zero", env: map[string]string{"MEMORY_LIMIT_BYTES": "0"}, wantErr: true},
		{name: "negative", env: map[string]string{"MEMORY_LIMIT_BYTES": "-1"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, ok, err := memoryLimit(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				assert.ErrorContains(t, err, "MEMORY_LIMIT_BYTES")
				assert.False(t, ok)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.limit, limit)
		})
	}
}
