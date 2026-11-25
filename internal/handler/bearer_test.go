package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header, token string
		ok            bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer   abc  ", "abc", true},
		{"Bearer", "", false},
		{"Bearer    ", "", false},
		{"Basic abc", "", false},
		{"Bearerabc", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		token, ok := bearerToken(tt.header)
		assert.Equal(t, tt.ok, ok, "%q", tt.header)
		assert.Equal(t, tt.token, token, "%q", tt.header)
	}
}

func TestBearerToken_ZeroAlloc(t *testing.T) {
	header := "Bearer some-long-tenant-token-value"
	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = bearerToken(header)
	})
	assert.Zero(t, allocs)
}
