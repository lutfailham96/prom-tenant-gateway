G=http://gw-main:8080
TA=e2e-token-tenant-a-0123456789abcdef0123456789
TB=e2e-token-tenant-b-0123456789abcdef0123456789

group "1. health & endpoints (gw-main)"
eq  "GET /healthz -> 200" 200 "$(code $G/healthz)"
h=$(curl -s $G/healthz)
eq  "healthz status" healthy "$(echo "$h" | jq -r .status)"
eq  "healthz version (ldflags)" e2e-test "$(echo "$h" | jq -r .version)"
[ -n "$(echo "$h" | jq -r '.uptime // empty')" ] && ok "healthz uptime set" || ko "healthz uptime set"
eq  "HEAD /healthz -> 200" 200 "$(code -I $G/healthz)"
eq  "/dashboard removed -> 404" 404 "$(code $G/dashboard)"
eq  "unknown path -> 404" 404 "$(code $G/does/not/exist)"
pc=$(code -X POST -H "Authorization: Bearer $TA" $G/validate-token)
eq  "POST /validate-token -> 405" 405 "$pc"
eq  "404 body is JSON" '"Cannot GET /dashboard"' "$(curl -s $G/dashboard | jq .error)"

group "2. token validation (gw-main)"
for i in 1 2 3; do eq "tenant a valid #$i -> 200" 200 "$(code -H "Authorization: Bearer $TA" $G/validate-token)"; done
eq  "tenant a X-Scope-OrgID" a "$(orgid -H "Authorization: Bearer $TA" $G/validate-token)"         # success a=4
eq  "tenant b X-Scope-OrgID" b "$(orgid -H "Authorization: Bearer $TB" $G/validate-token)"         # success b=1
eq  "lowercase bearer + spaces -> b" b "$(orgid -H "Authorization: bearer    $TB   " $G/validate-token)" # b=2
for i in 1 2 3 4 5; do eq "invalid token #$i -> 401" 401 "$(code -H 'Authorization: Bearer wrong-token-value-xyz' $G/validate-token)"; done
r=$(curl -s -D /tmp/h -H 'Authorization: Bearer wrong-token-value-xyz' $G/validate-token)          # invalid=6
eq  "invalid token body" "invalid token" "$(echo "$r" | jq -r .error)"
has "invalid token JSON content-type" "application/json" "$(cat /tmp/h)"
eq  "invalid token no OrgID (case-insens.)" "" "$(orgid -H 'Authorization: Bearer wrong-token-value-xyz' $G/validate-token)"  # invalid=7
eq  "missing header -> 401" 401 "$(code $G/validate-token)"
eq  "missing header body" "missing Authorization header" "$(curl -s $G/validate-token | jq -r .error)"   # missing=2
eq  "Basic scheme -> 401" 401 "$(code -H 'Authorization: Basic dXNlcjpwYXNz' $G/validate-token)"
eq  "Bearer w/o token -> 401" 401 "$(code -H 'Authorization: Bearer' $G/validate-token)"
eq  "truncated scheme -> 401" 401 "$(code -H 'Authorization: Bear' $G/validate-token)"
eq  "invalid header body" "invalid Authorization header" "$(curl -s -H 'Authorization: Token abc' $G/validate-token | jq -r .error)"  # invalid_header=4
eq  "token in query string ignored -> 401" 401 "$(code "$G/validate-token?token=$TA")"   # missing=3
head -c 5000 /dev/zero | tr '\0' a > /tmp/big; head -c 1000 /dev/zero | tr '\0' a > /tmp/small
eq  "body 5000B -> 413" 413 "$(code -X GET -H "Authorization: Bearer $TA" --data-binary @/tmp/big $G/validate-token)"
eq  "body 1000B -> 200" 200 "$(code -X GET -H "Authorization: Bearer $TA" --data-binary @/tmp/small $G/validate-token)"  # a=5

group "3. metrics (gw-main)"
A='prom_tenant_gateway_auth_requests_total'
eq "success tenant a"       5 "$(metric $G "$A{result=\"success\",tenant=\"a\"}")"
eq "success tenant b"       2 "$(metric $G "$A{result=\"success\",tenant=\"b\"}")"
eq "invalid_token"          7 "$(metric $G "$A{result=\"invalid_token\",tenant=\"\"}")"
eq "missing_header"         3 "$(metric $G "$A{result=\"missing_header\",tenant=\"\"}")"
eq "invalid_header"         4 "$(metric $G "$A{result=\"invalid_header\",tenant=\"\"}")"
eq "rate_limited (limiter off)" 0 "$(metric $G "$A{result=\"rate_limited\",tenant=\"\"}")"
eq "tokens_configured"      2 "$(metric $G prom_tenant_gateway_tokens_configured)"
H='prom_tenant_gateway_http_request_duration_seconds_count'
eq "histogram /validate-token 200" 7  "$(metric $G "$H{code=\"200\",method=\"GET\",route=\"/validate-token\"}")"
eq "histogram /validate-token 401" 14 "$(metric $G "$H{code=\"401\",method=\"GET\",route=\"/validate-token\"}")"
eq "histogram 404 unmatched" 3 "$(metric $G "$H{code=\"404\",method=\"GET\",route=\"unmatched\"}")"
out=$(curl -s $G/metrics)
info "405 series: $(echo "$out" | grep -F "$H" | grep 'code="405"' || echo none)"
info "413 series: $(echo "$out" | grep -F "$H" | grep 'code="413"' || echo none)"
has   "histogram /healthz present" 'route="/healthz"' "$out"
hasnt "no raw path label (/dashboard)" '/dashboard' "$out"
hasnt "no raw path label (/does/not)" '/does/not' "$out"
hasnt "old metric names gone" 'go_grafana_auth_' "$out"
has   "build_info" 'prom_tenant_gateway_build_info{goversion="go1.27' "$out"
has   "build_info version (ldflags)" 'version="e2e-test"} 1' "$out"
has   "process collector" 'process_start_time_seconds' "$out"
has   "go collector" 'go_goroutines' "$out"
hasnt "no token values in metrics" "$TA" "$out"

group "4. concurrency smoke (gw-main, 1000 req, 50 parallel)"
seq 1000 | xargs -P 50 -I{} curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TA" $G/validate-token > /tmp/conc
eq "1000 concurrent valid -> all 200" 1000 "$(grep -c '^200$' /tmp/conc)"
eq "success a after load" 1005 "$(metric $G "$A{result=\"success\",tenant=\"a\"}")"
summary main
