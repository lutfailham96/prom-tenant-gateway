---
name: docker-dind
description: "Run docker, compose, container builds, and E2E tests via the isolated docker-dind daemon."
---

# Docker-in-Docker (dind) Environment & E2E Testing Guide

Use this skill whenever building containers, testing docker compose stacks, or running end-to-end (E2E) integration tests on this machine.

## Architecture & Strict Policy

- **Host Docker (`docker-local.env`)**: Strict read-only socket (`/var/run/docker.sock`). Dilarang keras melakukan operasi mutating (build, run, create, up, down, rm).
- **Docker-in-Docker (`docker-dind.env`)**: Daemon `docker:29.10-dind` terisolasi via TLS port 2376. Semua operasi mutating, build image, dan E2E test container **wajib** dijalankan di sini.

## How to Activate

Before running any mutating `docker` or `docker compose` command, ALWAYS source the environment:

```bash
source /opt/data/profiles/devops/credentials/env/docker-dind.env
```

Verify connectivity:
```bash
source /opt/data/profiles/devops/credentials/env/docker-dind.env
docker version
```

## Standard E2E Testing Pattern

In `docker-dind`, containers run inside the dind daemon, so published ports (`ports: "8080:8080"`) are NOT accessible directly from host `localhost`. Tests MUST probe the service from an ephemeral test container connected to the same bridge network:

```bash
# 1. Load environment
source /opt/data/profiles/devops/credentials/env/docker-dind.env

# 2. Build and launch compose test stack
docker compose -f docker-compose.test.yml up --build -d

# 3. Wait until healthy
docker compose -f docker-compose.test.yml ps

# 4. Run assertions from a temporary container on the same network
docker run --rm --network <project>_test-network alpine sh -c '
  apk add --no-cache curl jq >/dev/null 2>&1

  # Test healthz
  curl -fsS http://<service-name>:8080/healthz

  # Test auth endpoint with valid token
  curl -i -s -H "Authorization: Bearer test-token-a" http://<service-name>:8080/validate-token

  # Test auth endpoint with invalid token (expects 401)
  curl -i -s -H "Authorization: Bearer wrong" http://<service-name>:8080/validate-token

  # Test Prometheus metrics
  curl -fsS http://<service-name>:8080/metrics
'

# 5. Clean up teardown
docker compose -f docker-compose.test.yml down -v
```

## Critical Rules & Pitfalls

1. **Healthcheck Loopback Address (`127.0.0.1` NOT `localhost`):**
   In Alpine-based images, busybox `wget` resolves `localhost` to IPv6 `::1` first. If the Go/FastHTTP/Fiber service listens on IPv4 (`0.0.0.0:8080`), connection to `[::1]:8080` is refused immediately, permanently failing healthchecks.
   - **Correct:** `test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1:8080/healthz"]`
   - **Avoid:** `http://localhost:8080/...`

2. **Host Bind Mounts Do NOT Work in dind:**
   Host filesystem paths do not exist inside the dind container.
   - For test fixtures or volume testing, use named Docker volumes:
     ```bash
     docker volume create test-data
     docker run --rm -v test-data:/data alpine sh -c 'echo "foo" > /data/bar'
     ```

3. **External Networks:**
   If a compose file references `external: true` networks (`web`, `db`, `monitoring`), create them inside dind first:
   ```bash
   source /opt/data/profiles/devops/credentials/env/docker-dind.env
   docker network create web 2>/dev/null || true
   ```

4. **Multi-Arch Builds:**
   When building Docker images, support BuildKit multi-arch targets:
   `ARG TARGETOS TARGETARCH` and `--platform=$BUILDPLATFORM golang:...`
