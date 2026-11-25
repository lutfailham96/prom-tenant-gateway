package auth_test

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/lutfailham96/prom-tenant-gateway/internal/auth"
	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *logger.Logger {
	return &logger.Logger{Logger: log.New(io.Discard, "", 0), Level: "error"}
}

// clearTokenEnv blanks every token variable inherited from the environment so
// tests do not depend on the machine they run on. Empty values are ignored by
// LoadFromEnv, and t.Setenv restores the originals afterwards.
func clearTokenEnv(t *testing.T) {
	t.Helper()
	t.Setenv("VALID_TOKENS_JSON", "")
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "TENANT_") {
			t.Setenv(key, "")
		}
	}
}

func loadFromEnv(t *testing.T) *auth.TokenStore {
	t.Helper()
	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.LoadFromEnv())
	return ts
}

func TestLoadFromEnv_TenantPattern(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_A_TOKEN", "token-a-123")
	t.Setenv("TENANT_B_TOKEN", "token-b-456")
	t.Setenv("TENANT_PROD_TOKEN", "prod-secret")

	ts := loadFromEnv(t)

	assert.Equal(t, 3, ts.Count())
	assert.Equal(t, "a", ts.Tokens()["token-a-123"])
	assert.Equal(t, "b", ts.Tokens()["token-b-456"])
	assert.Equal(t, "prod", ts.Tokens()["prod-secret"])
}

func TestLoadFromEnv_LegacyJSON(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("VALID_TOKENS_JSON", `{"tenant-legacy": "legacy-token-1", "another-tenant": "legacy-token-2"}`)

	ts := loadFromEnv(t)

	assert.Equal(t, 2, ts.Count())
	assert.Equal(t, "tenant-legacy", ts.Tokens()["legacy-token-1"])
	assert.Equal(t, "another-tenant", ts.Tokens()["legacy-token-2"])
}

func TestLoadFromEnv_CombinedPatterns(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_A_TOKEN", "new-token-a")
	t.Setenv("VALID_TOKENS_JSON", `{"legacy-tenant": "legacy-token"}`)

	ts := loadFromEnv(t)

	assert.Equal(t, 2, ts.Count())
	assert.Equal(t, "a", ts.Tokens()["new-token-a"])
	assert.Equal(t, "legacy-tenant", ts.Tokens()["legacy-token"])
}

func TestLoadFromEnv_Empty(t *testing.T) {
	clearTokenEnv(t)

	assert.Equal(t, 0, loadFromEnv(t).Count())
}

func TestLoadFromEnv_InvalidJSON(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("VALID_TOKENS_JSON", `{bad`)

	err := auth.NewTokenStore(discardLogger()).LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "VALID_TOKENS_JSON")
}

// Tokens from files/secrets with a trailing newline must still match the
// trimmed bearer token.
func TestLoadFromEnv_TrimsWhitespace(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_X_TOKEN", "  secret-x\n")

	tenant, ok := loadFromEnv(t).Validate("secret-x")

	assert.True(t, ok)
	assert.Equal(t, "x", tenant)
}

// TENANT_TOKEN and TENANT__TOKEN have no tenant ID and are ignored.
func TestLoadFromEnv_IgnoresMalformedEnvNames(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_TOKEN", "bare")
	t.Setenv("TENANT__TOKEN", "empty-id")

	ts := loadFromEnv(t)

	_, ok := ts.Validate("bare")
	assert.False(t, ok)
	_, ok = ts.Validate("empty-id")
	assert.False(t, ok)
}

func TestLoadFromEnv_RejectsInvalidTenantID(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("VALID_TOKENS_JSON", `{"a|b": "federated-token", "good": "good-token"}`)

	ts := auth.NewTokenStore(discardLogger())
	err := ts.LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a|b")
	_, ok := ts.Validate("federated-token")
	assert.False(t, ok)
	_, ok = ts.Validate("good-token")
	assert.True(t, ok, "valid entries are still loaded")
}

func TestLoadFromEnv_RejectsPlaceholderToken(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_PROD_TOKEN", "CHANGE_ME_PROD_TOKEN")

	err := auth.NewTokenStore(discardLogger()).LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TENANT_PROD_TOKEN")
	assert.Contains(t, err.Error(), "placeholder")
	assert.NotContains(t, err.Error(), "CHANGE_ME_PROD_TOKEN", "the token value must not be echoed")
}

func TestLoadFromEnv_WarnsOnShortToken(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("TENANT_SHORT_TOKEN", "short-secret")
	t.Setenv("TENANT_LONG_TOKEN", strings.Repeat("x", auth.MinRecommendedTokenLength))

	var buf bytes.Buffer
	ts := auth.NewTokenStore(&logger.Logger{Logger: log.New(&buf, "", 0), Level: "warn"})
	require.NoError(t, ts.LoadFromEnv(), "short tokens are accepted, only warned about")

	out := buf.String()
	assert.Equal(t, 2, ts.Count())
	assert.Equal(t, 1, strings.Count(out, "Token shorter than recommended length"))
	assert.Contains(t, out, `"tenant_id":"short"`)
	assert.NotContains(t, out, "short-secret", "the token value must not be logged")
}

func TestAdd_RejectsSharedToken(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.Add("same", "tenant-a"))
	assert.Error(t, ts.Add("same", "tenant-b"))
	assert.NoError(t, ts.Add("same", "tenant-a"), "re-adding the same mapping is fine")
}

func TestAdd_RejectsPlaceholderToken(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	assert.Error(t, ts.Add("CHANGE_ME", "a"))
	assert.Error(t, ts.Add("change_me_lowercase", "a"))
	assert.NoError(t, ts.Add("not-CHANGE_ME", "a"), "only the prefix is a placeholder")
}

func TestTokens_ReturnsCopy(t *testing.T) {
	ts := auth.NewTokenStore(discardLogger())
	require.NoError(t, ts.Add("tok", "a"))

	ts.Tokens()["injected"] = "evil"

	_, ok := ts.Validate("injected")
	assert.False(t, ok)
}

func TestValidateTenantID(t *testing.T) {
	for _, id := range []string{"a", "prod", "Tenant-1", "team_x.prod", "a!*'()"} {
		assert.NoError(t, auth.ValidateTenantID(id), id)
	}
	for _, id := range []string{"", ".", "..", "a|b", "has space", "slash/x", "ünï", strings.Repeat("a", 151)} {
		assert.Error(t, auth.ValidateTenantID(id), "%q", id)
	}
}

func TestExtractTenantIDFromEnvVar(t *testing.T) {
	tests := map[string]string{
		"TENANT_A_TOKEN":                "a",
		"TENANT_B_TOKEN":                "b",
		"TENANT_PROD_TOKEN":             "prod",
		"TENANT_STAGING_TOKEN":          "staging",
		"TENANT_my_custom_TENANT_TOKEN": "my_custom_tenant",
	}
	for envVar, want := range tests {
		t.Run(envVar, func(t *testing.T) {
			assert.Equal(t, want, auth.ExtractTenantIDFromEnvVar(envVar))
		})
	}
}
