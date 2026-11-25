TA=e2e-token-tenant-a-0123456789abcdef0123456789
group "7a. debug level traffic (gw-debug)"
eq "valid -> 200" 200 "$(code -H "Authorization: Bearer $TA" http://gw-debug:8080/validate-token)"
summary debug
