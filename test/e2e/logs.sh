# usage: logs.sh <instance>; stdin = docker logs output
cat > /tmp/log
TA=e2e-token-tenant-a-0123456789abcdef0123456789
TB=e2e-token-tenant-b-0123456789abcdef0123456789
L=/tmp/log
q(){ jq -c "select($1)" $L | wc -l | tr -d ' '; }
total=$(wc -l < $L | tr -d ' ')
valid=$(jq -c . $L 2>/dev/null | wc -l | tr -d ' ')
case "$1" in
main)
  group "8a. logs gw-main (LOG_LEVEL=info)"
  eq "every line valid JSON ($total lines)" "$total" "$valid"
  eq "every line has timestamp/level/message/service/version" "$total" "$(q '.timestamp and .level and .message and .service=="prom-tenant-gateway" and .version=="e2e-test"')"
  eq "no slog default keys (msg/time)" 0 "$(q 'has("msg") or has("time")')"
  eq "timestamp is UTC (Z suffix)" "$total" "$(q '.timestamp|endswith("Z")')"
  eq "level lowercase" "$total" "$(q '.level|test("^(debug|info|warn|error)$")')"
  eq "no debug lines at info level" 0 "$(q '.level=="debug"')"
  eq "no error lines" 0 "$(q '.level=="error"')"
  eq "successful auth not logged (status 200)" 0 "$(q '.status==200')"
  eq "probes not logged (/healthz,/metrics)" 0 "$(q '.path=="/healthz" or .path=="/metrics"')"
  eq "invalid_token: 1 warn line per request" 7 "$(q '.message=="HTTP request" and .level=="warn" and .auth_result=="invalid_token" and .status==401')"
  eq "missing_header warn lines" 3 "$(q '.level=="warn" and .auth_result=="missing_header"')"
  eq "invalid_header warn lines" 4 "$(q '.level=="warn" and .auth_result=="invalid_header"')"
  eq "404 logged at info (not error)" 3 "$(q '.status==404 and .level=="info"')"
  eq "405 logged at info" 1 "$(q '.status==405 and .level=="info"')"
  info "413 lines: $(q '.status==413') (body rejected by fasthttp before middleware?)"
  eq "no tenant_id on failures" 0 "$(q '.status==401 and has("tenant_id")')"
  eq "request lines have ip=172.30.0.20" "$(q '.message=="HTTP request"')" "$(q '.message=="HTTP request" and .ip=="172.30.0.20"')"
  eq "request lines have latency_ms number" "$(q '.message=="HTTP request"')" "$(q '.message=="HTTP request" and (.latency_ms|type)=="number"')"
  eq "user_agent captured" "$(q '.message=="HTTP request"')" "$(q '.message=="HTTP request" and (.user_agent|startswith("curl/"))')"
  eq "no token values in logs" 0 "$(grep -cF -e "$TA" -e "$TB" -e wrong-token-value-xyz $L)"
  eq "startup: Loaded tenant tokens count=2" 1 "$(q '.message=="Loaded tenant tokens" and .count==2')"
  eq "startup: Rate limiting disabled" 1 "$(q '.message=="Rate limiting disabled"')"
  eq "startup: TRUSTED_PROXIES warning" 1 "$(q '.level=="warn" and (.message|startswith("TRUSTED_PROXIES not set"))')"
  eq "startup: no weak-token warning (40-char tokens)" 0 "$(q '.message=="Token shorter than recommended length"')"
  eq "shutdown: signal received" 1 "$(q '.message|startswith("Shutdown signal received")')"
  eq "shutdown: complete" 1 "$(q '.message=="Shutdown complete"')"
  ;;
limit)
  group "8b. logs gw-limit"
  eq "every line valid JSON" "$total" "$valid"
  eq "rate_limited: 1 warn line per 429" 3 "$(q '.level=="warn" and .status==429 and .auth_result=="rate_limited"')"
  eq "invalid_token warn lines" 6 "$(q '.level=="warn" and .auth_result=="invalid_token"')"
  eq "weak-token warning emitted" 1 "$(q '.message=="Token shorter than recommended length" and .tenant_id=="a"')"
  eq "weak token value never logged" 0 "$(grep -cF short-token-a $L)"
  eq "startup: limiter config logged" 1 "$(q '.message=="Rate limiting enabled on /validate-token" and .max_failed_attempts==5 and .window=="5s"')"
  ;;
proxy)
  group "8c. logs gw-proxy (client IP resolution)"
  eq "untrusted peer logged as 172.30.0.20" 4 "$(q '.message=="HTTP request" and .ip=="172.30.0.20"')"
  eq "spoofed 1.1.1.x never logged as ip" 0 "$(q '.ip!=null and (.ip|startswith("1.1.1."))')"
  eq "trusted XFF client 9.9.9.1 logged (incl. forged left-most XFF)" 5 "$(q '.ip=="9.9.9.1"')"
  eq "trusted XFF client 9.9.9.2 logged" 1 "$(q '.ip=="9.9.9.2"')"
  eq "no TRUSTED_PROXIES warning" 0 "$(q '(.message|startswith("TRUSTED_PROXIES not set"))')"
  eq "proxy IP logged for no/invalid XFF from trusted proxy" 2 "$(q '.message=="HTTP request" and .ip=="172.30.0.10"')"
  ;;
debug)
  group "7b. logs gw-debug (LOG_LEVEL=debug)"
  eq "success logged at debug" 1 "$(q '.level=="debug" and .status==200 and .path=="/validate-token"')"
  eq "debug line has tenant_id=a" 1 "$(q '.level=="debug" and .tenant_id=="a" and .auth_result=="success"')"
  eq "probes still not logged at debug" 0 "$(q '.path=="/healthz"')"
  ;;
esac
summary "logs-$1"
