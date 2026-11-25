package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVersion(t *testing.T) {
	assert.Equal(t, "1.2.3", resolveVersion("1.2.3"), "ldflags value wins")
	assert.NotEmpty(t, resolveVersion("dev"))
	assert.NotEqual(t, "(devel)", resolveVersion("dev"))
}

// /healthz reports the build version, not a hardcoded string.
func TestNewApp_HealthReportsBuildVersion(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })
	version = "4.5.6-test"

	app := newTestApp(t, testConfig())
	resp := do(t, app, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "4.5.6-test", body["version"])
}
