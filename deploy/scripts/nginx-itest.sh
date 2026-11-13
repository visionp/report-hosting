#!/usr/bin/env bash
set -euo pipefail
for tool in nginx curl openssl; do
  command -v "$tool" >/dev/null || { echo "SKIP: nginx integration requires $tool"; exit 0; }
done
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
port=${REPORT_HOSTING_TEST_PORT:-14443}
cleanup() { nginx -p "$work/" -c "$work/nginx.conf" -s quit 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT
chmod 0755 "$work"
mkdir -p "$work/tls" "$work/data/sites" "$work/data/by-slug" "$work/snippets"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost -keyout "$work/tls/origin.key" -out "$work/tls/origin.pem" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=test-zone-ca -keyout "$work/tls/ca.key" -out "$work/tls/zone-client-ca.pem" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj /CN=test-client -keyout "$work/tls/client.key" -out "$work/tls/client.csr" >/dev/null 2>&1
openssl x509 -req -in "$work/tls/client.csr" -CA "$work/tls/zone-client-ca.pem" -CAkey "$work/tls/ca.key" -CAcreateserial -days 1 -out "$work/tls/client.pem" >/dev/null 2>&1
sed "s|/etc/report_hosting/tls|$work/tls|g" "$repo/deploy/nginx/tls.conf" > "$work/snippets/tls.conf"
cp "$repo/deploy/nginx/cloudflare-realip.conf" "$work/snippets/cloudflare-realip.conf"
sed -e "s|/etc/nginx/report_hosting.d|$work/snippets|g" \
    -e "s|/var/lib/report_hosting|$work/data|g" \
    -e "s|/var/log/nginx/report_hosting-access.log|$work/access.log|g" \
    -e "s|listen 443 ssl;|listen 127.0.0.1:$port ssl;|g" \
    -e '/listen \[::\]:443 ssl;/d' \
    -e 's|listen 80;|listen 127.0.0.1:18080;|g' \
    -e '/listen \[::\]:80;/d' "$repo/deploy/nginx/report_hosting.conf" > "$work/project.conf"
worker=""
if [ "$(id -u)" = 0 ]; then worker='user www-data;'; fi
cat > "$work/nginx.conf" <<EOF
$worker
pid $work/nginx.pid;
error_log $work/error.log warn;
events { worker_connections 128; }
http {
  include /etc/nginx/mime.types;
  default_type application/octet-stream;
  client_body_temp_path $work/client-body;
  proxy_temp_path $work/proxy-temp;
  include $work/project.conf;
}
EOF
make_site() {
  local site=$1 password=$2
  (umask 027; mkdir -p "$work/data/sites/$site/releases/000001"; printf '%s' "$site" > "$work/data/sites/$site/releases/000001/index.html")
  local hash
  hash=$({ printf '%s' "$password"; printf saltsalt; } | openssl dgst -sha1 -binary | { cat; printf saltsalt; } | openssl base64 -A)
  printf 'user:{SSHA}%s\n' "$hash" > "$work/data/sites/$site/.htpasswd"
  chmod 0640 "$work/data/sites/$site/.htpasswd"
  ln -s releases/000001 "$work/data/sites/$site/current"
  ln -s "../sites/$site" "$work/data/by-slug/$site"
}
make_site alpha alpha-password
make_site beta beta-password
ln -s index.html "$work/data/sites/alpha/releases/000001/linked.html"
if [ "$(id -u)" = 0 ]; then chgrp -R www-data "$work/data"; fi
nginx -p "$work/" -c "$work/nginx.conf" -t
nginx -p "$work/" -c "$work/nginx.conf"
request() {
  local host=$1 path=$2; shift 2
  curl --noproxy '*' -ksS --connect-timeout 3 --max-time 5 \
    --resolve "$host:$port:127.0.0.1" --cert "$work/tls/client.pem" --key "$work/tls/client.key" \
    -o /dev/null -w '%{http_code}' "$@" "https://$host:$port$path"
}
expect() {
  local want=$1 host=$2 path=$3; shift 3
  local got; got=$(request "$host" "$path" "$@")
  [ "$got" = "$want" ] || { echo "FAIL $host$path expected $want got $got"; cat "$work/error.log"; exit 1; }
  echo "PASS $host$path -> $want"
}
expect 200 alpha.partnersflex.space / -u user:alpha-password
expect 401 alpha.partnersflex.space / -u user:wrong-password
expect 401 beta.partnersflex.space / -u user:alpha-password
expect 401 alpha.partnersflex.space /
expect 404 unknown.partnersflex.space /
rm "$work/data/by-slug/beta"
expect 404 beta.partnersflex.space / -u user:beta-password
ln -s ../sites/alpha "$work/data/by-slug/gamma"
expect 200 gamma.partnersflex.space / -u user:alpha-password
expect 403 alpha.partnersflex.space /.htpasswd -u user:alpha-password
expect 403 alpha.partnersflex.space /.git/x -u user:alpha-password
expect 404 alpha.partnersflex.space /linked.html -u user:alpha-password
expect 404 partnersflex.space /
expect 404 nested.alpha.partnersflex.space /
expect 421 alpha.partnersflex.space / -H 'Host: admin.partnersflex.space'
expect 421 admin.partnersflex.space / -H 'Host: alpha.partnersflex.space'
request alpha.partnersflex.space / -u user:alpha-password -D "$work/site.headers" >/dev/null
grep -qi '^Cache-Control: private, no-store' "$work/site.headers"
request admin.partnersflex.space / -D "$work/admin.headers" >/dev/null
grep -qi '^X-Frame-Options: DENY' "$work/admin.headers"
grep -qi "frame-ancestors 'none'" "$work/admin.headers"
no_cert=$(curl --noproxy '*' -ksS --resolve "alpha.partnersflex.space:$port:127.0.0.1" -u user:alpha-password -o /dev/null -w '%{http_code}' "https://alpha.partnersflex.space:$port/" || true)
[ "$no_cert" != 200 ] || { echo 'FAIL connection without client cert succeeded'; exit 1; }
untrusted_cert=$(curl --noproxy '*' -ksS --resolve "alpha.partnersflex.space:$port:127.0.0.1" --cert "$work/tls/origin.pem" --key "$work/tls/origin.key" -u user:alpha-password -o /dev/null -w '%{http_code}' "https://alpha.partnersflex.space:$port/" || true)
[ "$untrusted_cert" != 200 ] || { echo 'FAIL untrusted client certificate succeeded'; exit 1; }
if grep -q 'No such file.*htpasswd\|htpasswd.*No such file' "$work/error.log"; then echo 'FAIL missing htpasswd log'; exit 1; fi
echo 'PASS auth isolation, no caching, hidden files, dynamic links, permissions, admin headers and local mTLS analog'
