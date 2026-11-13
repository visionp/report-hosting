#!/usr/bin/env bash
# Exercise the real executable, SQLite and HTTP listener on loopback only.
set -euo pipefail
binary=${1:?usage: app-smoke.sh /path/to/report_hosting}
work=$(mktemp -d)
pid=''
cleanup() { if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi; rm -rf "$work"; }
trap cleanup EXIT
export REPORT_HOSTING_DATA_DIR="$work/data"
export REPORT_HOSTING_PRODUCTION=false REPORT_HOSTING_ACCESS_CHECK=false
export REPORT_HOSTING_LISTEN_ADDR=127.0.0.1:18081
"$binary" check-config >/dev/null
printf '%s\n' 'local-smoke-password' | "$binary" admin create administrator >/dev/null
"$binary" serve >"$work/service.log" 2>&1 &
pid=$!
base=http://127.0.0.1:18081
for i in $(seq 1 50); do
  if curl -fsS -H 'Host: admin.partnersflex.space' "$base/login" > /dev/null 2>&1; then break; fi
  sleep .1
done
code=$(curl -sS -H 'Host: admin.partnersflex.space' -H 'Origin: https://admin.partnersflex.space' -d 'username=administrator&password=local-smoke-password' -D "$work/headers" -o /dev/null -w '%{http_code}' "$base/login")
[ "$code" = 303 ] || { echo "FAIL login: $code"; exit 1; }
cookie=$(sed -n 's/^Set-Cookie: \([^;]*\);.*/\1/p' "$work/headers" | tr -d '\r')
[ -n "$cookie" ] || { echo 'FAIL session cookie'; exit 1; }
token=$(printf 'report-hosting-csrf:%s' "${cookie#*=}" | sha256sum | cut -d' ' -f1)
request() { curl -sS -H 'Host: admin.partnersflex.space' -H 'Origin: https://admin.partnersflex.space' -H "Cookie: $cookie" -H "X-CSRF-Token: $token" "$@"; }
code=$(request -H 'Content-Type: application/json' -d '{"name":"Smoke site","slug":"smoke"}' -o "$work/site.json" -w '%{http_code}' "$base/api/sites")
[ "$code" = 201 ] || { echo "FAIL create: $code"; exit 1; }
id=$(sed -n 's/.*"id":"\([a-f0-9-]*\)".*/\1/p' "$work/site.json")
[ -n "$id" ] || { echo 'FAIL site ID'; exit 1; }
printf '<html>smoke release</html>' > "$work/index.html"
code=$(request -F "file=@$work/index.html" -o /dev/null -w '%{http_code}' "$base/api/sites/$id/deploy")
[ "$code" = 200 ] || { echo "FAIL deploy: $code"; exit 1; }
grep -q 'smoke release' "$work/data/by-slug/smoke/current/index.html"
request "$base/sites/$id" | grep -q 'Smoke site'
code=$(request -X POST -o /dev/null -w '%{http_code}' "$base/api/sites/$id/disable")
[ "$code" = 204 ] && [ ! -L "$work/data/by-slug/smoke" ] || { echo 'FAIL disable'; exit 1; }
code=$(request -X DELETE -H 'Content-Type: application/json' -d '{"slug":"smoke"}' -o /dev/null -w '%{http_code}' "$base/api/sites/$id")
[ "$code" = 204 ] || { echo "FAIL delete: $code"; exit 1; }
echo 'PASS executable config, admin creation, startup, login, create, HTML deploy, detail, disable and delete'
