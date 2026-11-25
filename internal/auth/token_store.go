package auth

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/lutfailham96/prom-tenant-gateway/internal/logger"
)

type TokenStore struct {
	tokens map[string]string
	logger *logger.Logger
}

func NewTokenStore(log *logger.Logger) *TokenStore {
	return &TokenStore{
		tokens: make(map[string]string),
		logger: log,
	}
}

func (s *TokenStore) LoadFromEnv() {
	// Support both patterns:
	// 1. Legacy: VALID_TOKENS_JSON (for backward compatibility)
	// 2. New: TENANT_A_TOKEN, TENANT_B_TOKEN, TENANT_C_TOKEN, etc.

	// Check legacy first
	if tokensJSON := os.Getenv("VALID_TOKENS_JSON"); tokensJSON != "" {
		var legacyTokens map[string]string
		if err := json.Unmarshal([]byte(tokensJSON), &legacyTokens); err != nil {
			s.logger.Warn("Failed to parse VALID_TOKENS_JSON", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			for tenant, token := range legacyTokens {
				if token != "" {
					s.tokens[token] = tenant
				}
			}
			s.logger.Info("Loaded tokens from VALID_TOKENS_JSON (legacy)", map[string]interface{}{
				"count": len(legacyTokens),
			})
		}
	}

	// Load from TENANT_*_TOKEN env vars (new pattern)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]

		if strings.HasPrefix(key, "TENANT_") && strings.HasSuffix(key, "_TOKEN") && value != "" {
			tenantPart := strings.TrimPrefix(key, "TENANT_")
			tenantPart = strings.TrimSuffix(tenantPart, "_TOKEN")
			tenantID := strings.ToLower(tenantPart)

			s.tokens[value] = tenantID
			s.logger.Info("Loaded token for tenant", map[string]interface{}{
				"tenant_id": tenantID,
				"source":    key,
			})
		}
	}

	s.logger.Info("Loaded tenant tokens", map[string]interface{}{
		"count": len(s.tokens),
	})
}

func (s *TokenStore) Validate(token string) (tenantID string, ok bool) {
	tenantID, ok = s.tokens[token]
	return
}

func (s *TokenStore) Count() int {
	return len(s.tokens)
}

func (s *TokenStore) Tokens() map[string]string {
	return s.tokens
}

func ExtractTenantIDFromEnvVar(envVar string) string {
	tenantPart := strings.TrimPrefix(envVar, "TENANT_")
	tenantPart = strings.TrimSuffix(tenantPart, "_TOKEN")
	return strings.ToLower(tenantPart)
}
