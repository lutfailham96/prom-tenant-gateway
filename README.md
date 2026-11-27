# prom-tenant-gateway

Lightweight authentication service for Grafana/Mimir multi-tenant environments. Validates Bearer tokens and injects `X-Scope-OrgID` header for downstream services.

## Features

- **Token validation** via `Authorization: Bearer <token>` header
- **Multi-tenant support** — maps tokens to tenant IDs (`X-Scope-OrgID`)
- **Two configuration patterns**:
  - `TENANT_<ID>_TOKEN=<token>` (new, recommended)
  - `VALID_TOKENS_JSON={"tenant": "token"}` (legacy)
- **Failed-auth rate limiting** per client IP (valid tokens are never limited)
- **Prometheus metrics** (`/metrics`)
- **Health check** (`/healthz`)
- **Structured JSON logging**

## Quick Start

### Local Development

```bash
# Run tests
go test -v ./...

# Build (version is injected at link time; defaults to "dev")
go build -trimpath -ldflags "-X main.version=$(git describe --tags --always)" -o bin/server ./cmd/server

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
# Build (BuildKit required for the cache mounts; default since Docker 23)
docker build --build-arg VERSION=1.2.3 -t prom-tenant-gateway .

# Run
docker run -d \
  -p 8080:8080 \
  -e TENANT_A_TOKEN=token-a \
  -e TENANT_B_TOKEN=token-b \
  -e RATE_LIMIT_MAX=100 \
  -e RATE_LIMIT_WINDOW=10s \
  prom-tenant-gateway
```

### E2E tests

See [Testing](#testing): `test/e2e/run.sh` builds the image and runs the full
end-to-end suite in Docker.

## API Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/validate-token` | GET | Required | Validates token, returns `X-Scope-OrgID` |
| `/healthz` | GET | None | Health check |
| `/metrics` | GET | None | Prometheus metrics |

### Responses of `/validate-token`

| Status | When | Headers / body |
|--------|------|----------------|
| `200` | Valid `Authorization: Bearer <token>` | `X-Scope-OrgID: <tenant-id>` |
| `401` | Missing/malformed header or unknown token | `{"error": "..."}` |
| `429` | Failed attempt from a client IP over the failure limit | `Retry-After: <seconds>`, `{"error":"rate limit exceeded"}` |

### Rate limiting

Only **failed** attempts (missing header, malformed header, invalid token) are
counted, per client IP, in a fixed window of `RATE_LIMIT_WINDOW` that starts at
the client's first failure. The first `RATE_LIMIT_MAX` failures get `401`; further
failures get `429` until the window ends.

The token is always validated first: a request with a **valid token is
accepted (200) even while its client IP is limited**, and it does not reset the
limit for that IP. A misbehaving client behind a shared NAT or proxy therefore
cannot lock out legitimate tenants. Successful requests never touch the limiter.

Trade-off: a limited client still learns when a guess is valid, so resistance
to token guessing comes from token entropy. Use long random tokens
(`openssl rand -hex 32`); tokens shorter than 32 characters are logged as weak
at startup.

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `8080` | HTTP server port |
| `TENANT_<ID>_TOKEN` | Yes* | — | Token for tenant `<ID>` (e.g., `TENANT_PROD_TOKEN=abc`) |
| `VALID_TOKENS_JSON` | Yes* | — | Legacy JSON map: `{"tenant": "token"}` |
| `RATE_LIMIT_ENABLED` | No | `true` | Enable the failed-auth rate limiter |
| `RATE_LIMIT_MAX` | No | `100` | Failed auth attempts allowed per client IP per window (must be > 0; valid tokens are never counted or limited) |
| `RATE_LIMIT_WINDOW` | No | `1m` | Rate limit window (Go duration, must be > 0) |
| `TRUSTED_PROXIES` | No | — | Comma-separated proxy IPs/CIDRs whose `X-Forwarded-For` is trusted (e.g. the ingress pod IPs). Invalid entries fail startup. When empty, `X-Forwarded-For` is ignored and clients behind a proxy share one failure bucket |
| `MEMORY_LIMIT_BYTES` | No | — | Container memory limit in bytes (Kubernetes Downward API `limits.memory`); the Go soft memory limit is set to 90% of it. Ignored when `GOMEMLIMIT` is set |

*At least one token configuration method required.

Startup fails if any token entry is invalid: tenant IDs must be 1–150 characters
of `[a-zA-Z0-9!-_.*'()]`, must not be `.`/`..` (Mimir rules; `|` is rejected
because it is Mimir's tenant-federation separator), and one token cannot map to
two tenants. Surrounding whitespace in tokens is trimmed.

Client IP resolution: `X-Forwarded-For` is only read when the TCP peer is in
`TRUSTED_PROXIES`. It is then walked from right to left, skipping trusted
proxies, and the first untrusted address is the client IP (an unparsable entry
stops the walk at the last trusted hop). Forged entries a client prepends are
therefore ignored, whether the proxy overwrites or appends to the header. Keep
`TRUSTED_PROXIES` as narrow as possible — never whole RFC1918 ranges, which
would let any pod forge its client IP.

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

The gateway itself (Deployment, Service, HPA, PDB, NetworkPolicy, ...) is in
`deploy/kubernetes/` (`kubectl apply -k deploy/kubernetes`).

### Gateway API (Traefik) — Recommended

Routes in `deploy/gateway-api/` attach to the shared Gateway
`traefik/traefik-internal-gateway` (not managed here):

```bash
kubectl apply -k deploy/gateway-api/
```

Components:
- `Middleware` (ForwardAuth) — Calls `/validate-token` on prom-tenant-gateway
- `HTTPRoute` (HTTPS) — `/api/v1/push` and `/prometheus` with auth
- `HTTPRoute` (HTTP) — Native `RequestRedirect` to HTTPS, no backends

See `deploy/gateway-api/README.md`.

### Legacy Ingress (nginx)

See `deploy/kubernetes/ingress-mimir-internal.yaml` (not in the kustomization).

## Project Structure

```
.
├── cmd/server/
│   ├── main.go                 # Entry point: config, token loading, signals
│   └── app.go                  # newApp(): Fiber config + routes (tested in app_test.go)
├── internal/
│   ├── auth/token_store.go     # Token storage & validation
│   ├── clientip/clientip.go    # Client IP behind trusted proxies (X-Forwarded-For)
│   ├── config/config.go        # Configuration
│   ├── handler/handler.go      # HTTP handlers
│   ├── logger/logger.go        # slog JSON logger
│   ├── metrics/metrics.go      # Prometheus metrics (client_golang, own registry)
│   ├── middleware/middleware.go # Request log + latency metrics
│   └── ratelimit/ratelimit.go  # Failed-auth limiter per client IP
├── deploy/
│   ├── kubernetes/             # Deployment, Service, HPA, PDB, ...
│   └── gateway-api/            # Kubernetes Gateway API manifests
├── test/e2e/                   # End-to-end suite (run.sh + compose.yml)
├── .dockerignore               # Keeps .git, deploy/, docs, token files out of the build context
├── Dockerfile                  # Multi-stage build
└── go.mod / go.sum             # Dependencies
```

Unit tests live next to the code they test (`*_test.go` in each package).

## Testing

```bash
# Unit tests (same flags as CI)
go test -race -count=1 ./...

# With coverage
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Lint and vulnerability scan (as in CI)
golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

# E2E (on this machine: against docker-dind)
source /opt/data/profiles/devops/credentials/env/docker-dind.env
test/e2e/run.sh            # KEEP=1 keeps the stack, NOCACHE=1 rebuilds
```

The E2E suite (`test/e2e/`) starts four gateway instances with separate
rate-limit state and checks, from probe containers with fixed trusted and
untrusted source IPs: token validation and status codes, body limit, exact
metric counts, the JSON log schema (one line per failed request, no token
values), rate limiting and window reset, `X-Forwarded-For` handling for trusted
and untrusted peers, startup config validation, the build version from
`-ldflags`, and graceful shutdown on SIGTERM. Exit code: 0 pass, 1 failures,
2 environment error.

CI (`.github/workflows/docker-build-push.yaml`) runs `gofmt`, `go mod tidy -diff`,
`go vet`, `go test -race` with coverage, `govulncheck`, and `golangci-lint`; the
image is only built and pushed when both the test and lint jobs pass.

## Metrics (Prometheus)

Served on `/metrics` by `prometheus/client_golang`:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `prom_tenant_gateway_auth_requests_total` | counter | `result`, `tenant` | Token validations. `result` is `success`, `invalid_token`, `invalid_header`, `missing_header` or `rate_limited`; `tenant` is set only for `success` |
| `prom_tenant_gateway_http_request_duration_seconds` | histogram | `route`, `method`, `code` | Request latency (buckets 100µs–1s). Unknown paths use `route="unmatched"` |
| `prom_tenant_gateway_tokens_configured` | gauge | — | Number of configured tokens |
| `prom_tenant_gateway_build_info` | gauge | `version`, `goversion` | Always 1 |
| `go_*`, `process_*` | — | — | Go runtime and process metrics (incl. `process_start_time_seconds`) |

### Migrating from the old metric names

The previous hand-written `go_grafana_auth_*` metrics were removed. Equivalent queries:

| Old | New |
|-----|-----|
| `go_grafana_auth_requests_total` | `sum(prom_tenant_gateway_auth_requests_total)` |
| `go_grafana_auth_requests_success_total` | `sum(prom_tenant_gateway_auth_requests_total{result="success"})` |
| `go_grafana_auth_requests_failed_total` | `sum(prom_tenant_gateway_auth_requests_total{result!="success"})` |
| `go_grafana_auth_uptime_seconds` | `time() - process_start_time_seconds` |
| `go_grafana_auth_tokens_configured` | `prom_tenant_gateway_tokens_configured` |

The old `failed` counter also counted unrelated errors (404s, oversized bodies) and
rate-limited requests without counting them in `total`; the new counter only
counts `/validate-token` outcomes, so `failed` can no longer exceed `total`.

## Logging

JSON lines on stdout (`log/slog`) with `timestamp`, `level`, `message`, `service`,
`version`. Every request produces one `HTTP request` line (`method`, `path`,
`status`, `latency_ms`, `ip`, `user_agent`, plus `auth_result` and `tenant_id` for
`/validate-token`), except `/healthz` and `/metrics`. The level follows the status:

| Status | Level |
|--------|-------|
| 2xx / 3xx | `debug` (successful auth traffic is high-volume; hidden at the default `info`) |
| 401, 403, 429 | `warn` |
| other 4xx | `info` |
| 5xx | `error` (plus a `Request error` line with the cause) |

Set `LOG_LEVEL=debug` to also log successful requests.

## Building

```bash
# Local
go build -ldflags="-s -w" -o bin/server ./cmd/server

# Docker multi-stage (production)
docker build -t prom-tenant-gateway .
```

## License

MIT