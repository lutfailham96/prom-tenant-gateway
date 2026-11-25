#!/usr/bin/env bash
# End-to-end tests for prom-tenant-gateway.
#
# Builds the runner image (BuildKit), starts four gateway instances
# (compose.yml) and asserts HTTP behaviour, metrics, logs, startup validation
# and graceful shutdown from probe containers on the same network.
#
# Usage (on this machine all mutating docker commands go to docker-dind):
#   source /opt/data/profiles/devops/credentials/env/docker-dind.env
#   test/e2e/run.sh
# or: DIND_ENV=/path/to/docker-dind.env test/e2e/run.sh
#
# Env: KEEP=1 keeps the stack after the run, NOCACHE=1 rebuilds without cache.
# Exit: 0 all passed, 1 assertion failures, 2 environment/setup error.
set -uo pipefail
cd "$(dirname "$0")"
# shellcheck disable=SC1090
[ -n "${DIND_ENV:-}" ] && source "$DIND_ENV"
docker version >/dev/null 2>&1 || { echo "docker daemon unreachable (source the docker-dind env?)"; exit 2; }
docker compose version >/dev/null 2>&1 || { echo "docker compose v2 required"; exit 2; }

DC="docker compose -f compose.yml"
NET=ptg-e2e_e2e
PROBE=ptg-e2e-probe:local
TMP=$(mktemp -d)
cleanup(){ [ "${KEEP:-0}" = 1 ] || $DC down -v --remove-orphans >/dev/null 2>&1; rm -rf "$TMP"; }
trap cleanup EXIT
rc=0

echo "### build"
# Probe image (curl + jq). Built with run+commit rather than docker build:
# DNS inside legacy-builder containers is unreliable on docker-dind.
if ! docker image inspect $PROBE >/dev/null 2>&1; then
  docker network create ptg-e2e-bootstrap >/dev/null 2>&1
  docker rm -f ptg-e2e-probe-build >/dev/null 2>&1
  docker run --name ptg-e2e-probe-build --network ptg-e2e-bootstrap alpine:3.23 sh -c 'apk add --no-cache curl jq >/dev/null' &&
    docker commit ptg-e2e-probe-build $PROBE >/dev/null
  ok=$?; docker rm -f ptg-e2e-probe-build >/dev/null 2>&1; docker network rm ptg-e2e-bootstrap >/dev/null 2>&1
  [ $ok = 0 ] || { echo "probe image build failed"; exit 2; }
fi
$DC build ${NOCACHE:+--no-cache} gw-main >"$TMP/build.log" 2>&1 || { tail -30 "$TMP/build.log"; exit 2; }
docker image inspect -f 'image {{.Id}} created {{.Created}}' prom-tenant-gateway:e2e

echo "### up"
$DC up -d --wait --wait-timeout 90 >"$TMP/up.log" 2>&1 || { cat "$TMP/up.log"; $DC ps; $DC logs; exit 2; }
$DC ps --format '{{.Name}}  {{.Status}}'

# probe <source ip> <script>: run an assertion script inside the network.
probe(){ cat lib.sh "$2" | docker run --rm -i --network $NET --ip "$1" $PROBE sh -s || rc=1; }
# logcheck <service> <mode>: assert on a service's JSON log output.
logcheck(){ $DC logs --no-color --no-log-prefix "$1" 2>/dev/null |
  docker run --rm -i -e SCRIPT="$(cat lib.sh logs.sh)" $PROBE sh -c 'printf "%s\n" "$SCRIPT" > /tmp/s.sh; sh /tmp/s.sh "$0"' "$2" || rc=1; }

probe 172.30.0.20 probe-main.sh
probe 172.30.0.20 probe-limit.sh
probe 172.30.0.20 probe-proxy-untrusted.sh
probe 172.30.0.10 probe-proxy-trusted.sh
probe 172.30.0.20 probe-debug.sh
logcheck gw-debug debug

echo; echo "== 10. graceful shutdown (SIGTERM gw-main)"
t0=$(date +%s); $DC stop -t 15 gw-main >/dev/null 2>&1; t1=$(date +%s)
ec=$(docker inspect -f '{{.State.ExitCode}}' ptg-e2e-gw-main-1); dur=$((t1-t0))
if [ "$ec" = 0 ]; then echo "PASS  exit code 0"; else echo "FAIL  exit code $ec"; rc=1; fi
if [ "$dur" -lt 10 ]; then echo "PASS  stopped in ${dur}s (<10s)"; else echo "FAIL  stop took ${dur}s"; rc=1; fi

logcheck gw-main main
logcheck gw-limit limit
logcheck gw-proxy proxy
bash startup.sh || rc=1

echo; [ $rc = 0 ] && echo "### OVERALL: PASS" || echo "### OVERALL: FAIL"
exit $rc
