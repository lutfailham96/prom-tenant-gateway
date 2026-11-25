package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
)

const (
	envPrefix = "TENANT_"
	envSuffix = "_TOKEN"

	// maxTenantIDLength mirrors Mimir's tenant ID length limit.
	maxTenantIDLength = 150

	// placeholderPrefix marks the example values shipped in
	// deploy/kubernetes/02-secret.example.yaml; they must never be accepted.
	placeholderPrefix = "CHANGE_ME"

	// MinRecommendedTokenLength is the length below which a token is
	// accepted but logged as weak.
	MinRecommendedTokenLength = 32
)

type TokenStore struct {
	tokens map[string]string
	logger *slog.Logger
}

func NewTokenStore(log *slog.Logger) *TokenStore {
	return &TokenStore{
		tokens: make(map[string]string),
		logger: log,
	}
}

// ValidateTenantID enforces Mimir's tenant ID rules: 1-150 characters from
// [a-zA-Z0-9!-_.*'()], and not "." or "..". The '|' character is rejected
// because Mimir uses it as the tenant federation separator, which would let a
// single token read several tenants.
func ValidateTenantID(id string) error {
	if id == "" {
		return errors.New("tenant ID is empty")
	}
	if len(id) > maxTenantIDLength {
		return fmt.Errorf("tenant ID %q exceeds %d characters", id, maxTenantIDLength)
	}
	if id == "." || id == ".." {
		return fmt.Errorf("tenant ID %q is not allowed", id)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!-_.*'()", c) >= 0:
		default:
			return fmt.Errorf("tenant ID %q contains invalid character %q", id, c)
		}
	}
	return nil
}

// Add registers a token for a tenant. The token is trimmed of surrounding
// whitespace (secrets created from files often carry a trailing newline).
func (s *TokenStore) Add(token, tenantID string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("empty token for tenant %q", tenantID)
	}
	if len(token) >= len(placeholderPrefix) && strings.EqualFold(token[:len(placeholderPrefix)], placeholderPrefix) {
		return fmt.Errorf("tenant %q uses a placeholder token (%s...); set a real secret", tenantID, placeholderPrefix)
	}
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	if existing, ok := s.tokens[token]; ok && existing != tenantID {
		return fmt.Errorf("token is shared by tenants %q and %q", existing, tenantID)
	}
	s.tokens[token] = tenantID
	return nil
}

// LoadFromEnv loads tokens from VALID_TOKENS_JSON (legacy) and
// TENANT_<ID>_TOKEN variables. Any invalid entry is an error so a
// misconfiguration fails at startup instead of silently dropping a tenant.
func (s *TokenStore) LoadFromEnv() error {
	var errs []error

	if tokensJSON := os.Getenv("VALID_TOKENS_JSON"); tokensJSON != "" {
		var legacyTokens map[string]string
		if err := json.Unmarshal([]byte(tokensJSON), &legacyTokens); err != nil {
			errs = append(errs, fmt.Errorf("parse VALID_TOKENS_JSON: %w", err))
		} else {
			loaded := 0
			for tenant, token := range legacyTokens {
				if strings.TrimSpace(token) == "" {
					continue
				}
				if err := s.Add(token, tenant); err != nil {
					errs = append(errs, fmt.Errorf("VALID_TOKENS_JSON: %w", err))
					continue
				}
				s.warnIfWeak(token, tenant, "VALID_TOKENS_JSON")
				loaded++
			}
			s.logger.Info("Loaded tokens from VALID_TOKENS_JSON (legacy)", slog.Int("count", loaded))
		}
	}

	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if !ok || !isTenantTokenEnv(key) || strings.TrimSpace(value) == "" {
			continue
		}
		tenantID := ExtractTenantIDFromEnvVar(key)
		if err := s.Add(value, tenantID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		s.warnIfWeak(value, tenantID, key)
		s.logger.Info("Loaded token for tenant", slog.String("tenant_id", tenantID), slog.String("source", key))
	}

	s.logger.Info("Loaded tenant tokens", slog.Int("count", len(s.tokens)))
	return errors.Join(errs...)
}

// warnIfWeak logs (without the token value) when a token is shorter than
// MinRecommendedTokenLength.
func (s *TokenStore) warnIfWeak(token, tenantID, source string) {
	if n := len(strings.TrimSpace(token)); n < MinRecommendedTokenLength {
		s.logger.Warn("Token shorter than recommended length",
			slog.String("tenant_id", tenantID),
			slog.String("source", source),
			slog.Int("length", n),
			slog.Int("min_length", MinRecommendedTokenLength),
		)
	}
}

// isTenantTokenEnv reports whether key has the form TENANT_<ID>_TOKEN with a
// non-empty <ID>. A bare TENANT_TOKEN does not qualify.
func isTenantTokenEnv(key string) bool {
	return len(key) > len(envPrefix)+len(envSuffix) &&
		strings.HasPrefix(key, envPrefix) &&
		strings.HasSuffix(key, envSuffix)
}

func (s *TokenStore) Validate(token string) (tenantID string, ok bool) {
	tenantID, ok = s.tokens[token]
	return
}

func (s *TokenStore) Count() int {
	return len(s.tokens)
}

// Tenants returns the distinct tenant IDs, sorted.
func (s *TokenStore) Tenants() []string {
	seen := make(map[string]struct{}, len(s.tokens))
	out := make([]string, 0, len(s.tokens))
	for _, tenant := range s.tokens {
		if _, ok := seen[tenant]; !ok {
			seen[tenant] = struct{}{}
			out = append(out, tenant)
		}
	}
	slices.Sort(out)
	return out
}

// Tokens returns a copy of the token → tenant mapping.
func (s *TokenStore) Tokens() map[string]string {
	out := make(map[string]string, len(s.tokens))
	for k, v := range s.tokens {
		out[k] = v
	}
	return out
}

func ExtractTenantIDFromEnvVar(envVar string) string {
	tenantPart := strings.TrimPrefix(envVar, envPrefix)
	tenantPart = strings.TrimSuffix(tenantPart, envSuffix)
	return strings.ToLower(tenantPart)
}
