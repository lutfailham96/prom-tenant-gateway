# Project: prom-tenant-gateway

## Docker & E2E Testing
- Host Docker socket is strictly read-only.
- All mutating docker operations (build, run, compose, E2E test) MUST run against `docker-dind`:
  ```bash
  source /opt/data/profiles/devops/credentials/env/docker-dind.env
  ```
- E2E suite: `test/e2e/run.sh` (after sourcing the dind env). It builds the image, starts the stack from `test/e2e/compose.yml` and runs assertions from probe containers on the `ptg-e2e_e2e` network.
- In E2E tests, service ports are internal to dind. Run test assertions from an ephemeral container attached to the same network.
- Healthcheck commands must use `127.0.0.1`, never `localhost` (prevents IPv6 `::1` connection refused in Alpine).

## Git Commit Rules
- Never add `Co-Authored-By` (Claude, AI, or bot) to git commit messages or PR descriptions.
