# prom-tenant-gateway

Lightweight authentication service for Grafana/Mimir multi-tenant environments. Validates Bearer tokens and injects `X-Scope-OrgID` header for downstream services.

## Features

- **Token validation** via `Authorization: Bearer <token>` header
- **Multi-tenant support** — maps tokens to tenant IDs (`X-Scope-OrgID`)
- **Two configuration patterns**:
  - `TENANT_<ID>_TOKEN=<token>` (new, recommended)
  - `VALID_TOKENS_JSON={"tenant": "token"}` (legacy)
- **Rate limiting** (configurable requests/window)
- **Prometheus metrics** (`/metrics`)
- **Health check** (`/healthz`)
- **Structured JSON logging**

## Quick Start

### Local Development

```bash
# Run tests
go test -v ./...

# Build
go build -o bin/server ./cmd/server

# Run with environment variables
TENANT_A_TOKEN=token-a \
TENANT_B_TOKEN=token-b \
PORT=8080 \
RATE_LIMIT_MAX=100 \
RATE_LIMIT_WINDOW=10s \
./bin/server
```

### Docker

```bash
# Build
docker build -t prom-tenant-gateway .

# Run
docker run -d \
  -p 8080:8080 \
  -e TENANT_A_TOKEN=token-a \
  -e TENANT_B_TOKEN=token-b \
  -e RATE_LIMIT_MAX=100 \
  -e RATE_LIMIT_WINDOW=10s \
  prom-tenant-gateway
```

### Docker Compose (E2E Test)

```bash
# Requires docker-dind environment
source /opt/data/profiles/devops/credentials/env/docker-dind.env
docker compose -f docker-compose.test.yml up --build -d

# Test endpoints
curl -H "Authorization: Bearer test-token-a" http://localhost:8080/validate-token
curl http://localhost:8080/healthz
curl http://localhost:8080/metrics
```

## API Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/validate-token` | GET | Required | Validates token, returns `X-Scope-OrgID` |
| `/healthz` | GET | None | Health check |
| `/metrics` | GET | None | Prometheus metrics |

### Response Headers (on `/validate-token` success)

```
X-Scope-OrgID: <tenant-id>
X-RateLimit-Limit: <max-requests>
X-RateLimit-Remaining: <remaining>
X-RateLimit-Reset: <seconds>
```

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `8080` | HTTP server port |
| `TENANT_<ID>_TOKEN` | Yes* | — | Token for tenant `<ID>` (e.g., `TENANT_PROD_TOKEN=abc`) |
| `VALID_TOKENS_JSON` | Yes* | — | Legacy JSON map: `{"tenant": "token"}` |
| `RATE_LIMIT_MAX` | No | `100` | Max **failed** auth attempts per client IP per window (successful requests are not counted) |
| `RATE_LIMIT_WINDOW` | No | `10s` | Rate limit window (Go duration) |
| `TRUSTED_PROXIES` | No | — | Comma-separated proxy IPs/CIDRs whose `X-Forwarded-For` is trusted (e.g. the ingress pod CIDR). When empty, `X-Forwarded-For` is ignored and all clients behind a proxy share one rate-limit bucket |

*At least one token configuration method required.

Startup fails if any token entry is invalid: tenant IDs must be 1–150 characters
of `[a-zA-Z0-9!-_.*'()]`, must not be `.`/`..` (Mimir rules; `|` is rejected
because it is Mimir's tenant-federation separator), and one token cannot map to
two tenants. Surrounding whitespace in tokens is trimmed.

The proxies listed in `TRUSTED_PROXIES` must overwrite (not append to) a
client-supplied `X-Forwarded-For`; the left-most valid IP is used as the client
IP. ingress-nginx does this by default (`use-forwarded-headers: false`), as does
Traefik for untrusted entrypoints.

### Token Patterns

**New pattern (recommended):**
```bash
TENANT_A_TOKEN=token-a
TENANT_B_TOKEN=token-b
TENANT_PROD_TOKEN=prod-secret
```
Maps to tenant IDs: `a`, `b`, `prod`

**Legacy pattern:**
```bash
VALID_TOKENS_JSON='{"tenant-legacy": "legacy-token-1", "another-tenant": "legacy-token-2"}'
```

**Combined:** Both patterns work simultaneously.

## Kubernetes Deployment

### Gateway API (Traefik) — Recommended

Native Kubernetes Gateway API manifests in `deploy/gateway-api/`:

```bash
# 1. GatewayClass (cluster-scoped, once per cluster)
kubectl apply -f deploy/gateway-api/00-gatewayclass.yaml

# 2. Namespace resources
kubectl apply -k deploy/gateway-api/
```

Components:
- `Gateway` — HTTP + HTTPS listeners with TLS termination (cert-manager)
- `Middleware` (ForwardAuth) — Calls `/validate-token` on prom-tenant-gateway
- `Middleware` (RedirectScheme) — HTTP → HTTPS redirect
- `HTTPRoute` (HTTPS) — Routes `/api/v1/push` with auth
- `HTTPRoute` (HTTP) — Redirects to HTTPS
- `ReferenceGrant` — Allows HTTPRoute to reference Middleware

### Legacy Ingress (nginx)

See `deploy/ingress/` for old nginx Ingress manifests.

## Project Structure

```
.
├── cmd/server/main.go          # Entry point
├── internal/
│   ├── auth/token_store.go     # Token storage & validation
│   ├── config/config.go        # Configuration
│   ├── handler/handler.go      # HTTP handlers
│   ├── logger/logger.go        # JSON logger
│   ├── metrics/metrics.go      # Prometheus metrics
│   └── middleware/middleware.go # Rate limiting, request logging
├── deploy/
│   └── gateway-api/            # Kubernetes Gateway API manifests
├── docker-compose.test.yml     # E2E test compose
├── Dockerfile                  # Multi-stage build
├── go.mod / go.sum             # Dependencies
└── main_test.go                # Comprehensive tests
```

## Testing

```bash
# Unit tests
go test -v ./...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# E2E via docker-dind
source /opt/data/profiles/devops/credentials/env/docker-dind.env
docker compose -f docker-compose.test.yml up --build -d
# Run curl tests against localhost:8080
docker compose -f docker-compose.test.yml down
```

## Metrics (Prometheus)

```
go_grafana_auth_requests_total              # Total requests
go_grafana_auth_requests_success_total      # Successful validations
go_grafana_auth_requests_failed_total       # Failed validations
go_grafana_auth_uptime_seconds              # Service uptime
go_grafana_auth_tokens_configured           # Number of valid tokens
```

## Building

```bash
# Local
go build -ldflags="-s -w" -o bin/server ./cmd/server

# Docker multi-stage (production)
docker build -t prom-tenant-gateway .
```

## License

MIT