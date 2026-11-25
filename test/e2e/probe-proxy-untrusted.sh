G=http://gw-proxy:8080
group "6a. untrusted peer 172.30.0.20 spoofing XFF (gw-proxy: max 3)"
for i in 1 2 3; do eq "spoofed XFF 1.1.1.$i -> 401" 401 "$(code -H "X-Forwarded-For: 1.1.1.$i" -H 'Authorization: Bearer nope' $G/validate-token)"; done
eq "4th with new spoofed XFF -> 429 (XFF ignored)" 429 "$(code -H 'X-Forwarded-For: 1.1.1.4' -H 'Authorization: Bearer nope' $G/validate-token)"
summary proxy-untrusted
