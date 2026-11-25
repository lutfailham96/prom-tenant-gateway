G=http://gw-proxy:8080
group "6b. trusted proxy 172.30.0.10 forwarding XFF (gw-proxy: max 3)"
for i in 1 2 3; do eq "client 9.9.9.1 failed #$i -> 401" 401 "$(code -H 'X-Forwarded-For: 9.9.9.1' -H 'Authorization: Bearer nope' $G/validate-token)"; done
eq "client 9.9.9.1 4th -> 429" 429 "$(code -H 'X-Forwarded-For: 9.9.9.1' -H 'Authorization: Bearer nope' $G/validate-token)"
eq "client 9.9.9.2 separate bucket -> 401" 401 "$(code -H 'X-Forwarded-For: 9.9.9.2' -H 'Authorization: Bearer nope' $G/validate-token)"
eq "proxy itself (no XFF) separate bucket -> 401" 401 "$(code -H 'Authorization: Bearer nope' $G/validate-token)"
eq "invalid XFF value falls back -> 401" 401 "$(code -H 'X-Forwarded-For: not-an-ip' -H 'Authorization: Bearer nope' $G/validate-token)"
summary proxy-trusted
