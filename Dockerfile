# syntax=docker/dockerfile:1
# Requires BuildKit (default in Docker >= 23, docker compose and buildx):
# the RUN --mount cache mounts are not supported by the legacy builder.

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
# Reported by /healthz and the prom_tenant_gateway_build_info metric.
# CI sets it from the git tag (docker/metadata-action version output).
ARG VERSION=dev
WORKDIR /app
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/prom-tenant-gateway ./cmd/server

# Test stage: docker build --target tester .
FROM builder AS tester
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test -coverprofile=/tmp/coverage.out ./... && \
    go tool cover -func=/tmp/coverage.out | tail -1

# alpine (not distroless) on purpose: the Kubernetes preStop hook runs
# /bin/sleep and the compose healthcheck uses busybox wget.
FROM alpine:3.23 AS runner
ARG VERSION=dev
LABEL org.opencontainers.image.version=${VERSION}
RUN adduser -D -H -u 10001 appuser
COPY --from=builder /out/prom-tenant-gateway /usr/local/bin/prom-tenant-gateway
# Numeric UID so the kubelet can verify runAsNonRoot without /etc/passwd.
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/prom-tenant-gateway"]
