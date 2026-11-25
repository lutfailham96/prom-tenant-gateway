#!/bin/sh
# Assertion helpers, prepended to every probe script (busybox sh).
PASS=0; FAIL=0
group(){ echo; echo "== $1"; }
ok(){ PASS=$((PASS+1)); echo "PASS  $1"; }
ko(){ FAIL=$((FAIL+1)); echo "FAIL  $1"; }
eq(){ if [ "$2" = "$3" ]; then ok "$1"; else ko "$1 (expected=$2 got=$3)"; fi; }
has(){ case "$3" in *"$2"*) ok "$1";; *) ko "$1 (missing: $2)";; esac; }
hasnt(){ case "$3" in *"$2"*) ko "$1 (unexpected: $2)";; *) ok "$1";; esac; }
info(){ echo "INFO  $1"; }
summary(){ echo; echo "RESULT[$1]: $PASS passed, $FAIL failed"; [ "$FAIL" -eq 0 ]; }
code(){ curl -s -o /dev/null -w '%{http_code}' "$@"; }
orgid(){ curl -s -D - -o /dev/null "$@" | tr -d '\r' | awk 'tolower($1)=="x-scope-orgid:"{print $2}'; }
# metric <base> <exact series selector> -> value (empty if absent)
metric(){ curl -s "$1/metrics" | awk -v k="$2" '$1==k{print $2; exit}'; }
