# runs on host (dind client); checks config validation exit codes
. ./lib.sh
IMG=prom-tenant-gateway:e2e
group "9. startup config validation (docker run, expect exit 1)"
run(){ # name, expected substring, env...
  name=$1; want=$2; shift 2
  out=$(docker run --rm "$@" $IMG 2>&1); rc=$?
  eq "$name -> exit 1" 1 "$rc"
  has "$name -> error message" "$want" "$out"
}
run "federated tenant a|b (JSON)" "contains invalid character '|'" -e 'VALID_TOKENS_JSON={"a|b":"tok-0123456789abcdef0123456789abcdef"}'
run "broken JSON" "parse VALID_TOKENS_JSON" -e 'VALID_TOKENS_JSON={bad'
run "token shared by 2 tenants" "is shared by tenants" -e TENANT_A_TOKEN=same-0123456789abcdef0123456789abcdef -e TENANT_B_TOKEN=same-0123456789abcdef0123456789abcdef
run "tenant ID \"..\" (path traversal)" "is not allowed" -e 'VALID_TOKENS_JSON={"..":"tok-0123456789abcdef0123456789abcdef"}'
run "no tokens" "No valid tokens configured"
run "only TENANT_TOKEN (no tenant part)" "No valid tokens configured" -e TENANT_TOKEN=x0123456789abcdef0123456789abcdef
run "CHANGE_ME placeholder (env)" "placeholder" -e TENANT_PROD_TOKEN=CHANGE_ME_PROD_TOKEN
run "change_me placeholder (JSON, lowercase)" "placeholder" -e 'VALID_TOKENS_JSON={"x":"change_me"}'
summary startup
