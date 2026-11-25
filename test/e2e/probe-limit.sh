G=http://gw-limit:8080
group "5. rate limit (gw-limit: max 5 failed / 5s)"
n=0; for i in $(seq 20); do [ "$(code -H 'Authorization: Bearer short-token-a' $G/validate-token)" = 200 ] && n=$((n+1)); done
eq "20 valid requests never limited" 20 "$n"
n=0; for i in 1 2 3 4 5; do [ "$(code -H 'Authorization: Bearer nope' $G/validate-token)" = 401 ] && n=$((n+1)); done
eq "failed attempts 1-5 -> 401" 5 "$n"
r=$(curl -s -D /tmp/h -H 'Authorization: Bearer nope' $G/validate-token)
has "6th failed -> 429" " 429" "$(head -1 /tmp/h)"
eq  "429 body" "rate limit exceeded" "$(echo "$r" | jq -r .error)"
has "429 JSON content-type" "application/json" "$(cat /tmp/h)"
eq  "valid token same IP while limited -> 429 (by design)" 429 "$(code -H 'Authorization: Bearer short-token-a' $G/validate-token)"
eq  "healthz unaffected while limited" 200 "$(code $G/healthz)"
A='prom_tenant_gateway_auth_requests_total'
eq "metric rate_limited" 2 "$(metric $G "$A{result=\"rate_limited\",tenant=\"\"}")"
eq "metric invalid_token" 5 "$(metric $G "$A{result=\"invalid_token\",tenant=\"\"}")"
eq "metric success a" 20 "$(metric $G "$A{result=\"success\",tenant=\"a\"}")"
sleep 6
eq "after window: valid -> 200" 200 "$(code -H 'Authorization: Bearer short-token-a' $G/validate-token)"
eq "after window: failed -> 401 again" 401 "$(code -H 'Authorization: Bearer nope' $G/validate-token)"
summary limit
