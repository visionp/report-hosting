#!/usr/bin/env bash
# Read-only live checks. This script never creates, changes or deletes sites.
set -euo pipefail
command -v curl >/dev/null || { echo 'FAIL curl is required'; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
host=${REPORT_HOSTING_SITE_HOST:-}
user=${REPORT_HOSTING_SITE_USERNAME:-}
password=${REPORT_HOSTING_SITE_PASSWORD:-}
origin4=${REPORT_HOSTING_ORIGIN_IPV4:-}
origin6=${REPORT_HOSTING_ORIGIN_IPV6:-}

status() {
  curl --noproxy '*' -sS --connect-timeout 5 --max-time 15 -o /dev/null -w '%{http_code}' "$@"
}
admin=$(status https://admin.partnersflex.space/)
case "$admin" in 301|302|303|307|308|401|403) echo 'PASS AC-01 partial: unauthenticated admin content is not public';;
  *) echo "FAIL unauthenticated admin status $admin"; exit 1;;
esac
if [ -n "$host" ]; then
  [[ "$host" =~ ^[a-z0-9][a-z0-9-]*\.partnersflex\.space$ ]] && [ "$host" != admin.partnersflex.space ] || { echo 'FAIL invalid test site host'; exit 1; }
  code=$(status "https://$host/")
  [ "$code" = 401 ] || { echo "FAIL site without credentials: $code"; exit 1; }
  echo 'PASS AC-06 partial: deployed test site requires credentials'
  if [ -n "$user" ] && [ -n "$password" ]; then
    [[ "$user" =~ ^[a-z0-9-]{1,32}$ ]] && [[ "$password" =~ ^[A-Za-z0-9_-]{20,}$ ]] || { echo 'FAIL credentials must use the generated site format'; exit 1; }
    # curl configuration on stdin keeps credentials out of command arguments.
    auth=$(printf 'user = "%s:%s"\n' "$user" "$password" | curl --config - --noproxy '*' -sS --connect-timeout 5 --max-time 15 -D "$work/headers" -o /dev/null -w '%{http_code}' "https://$host/")
    [ "$auth" = 200 ] || { echo "FAIL correct site credentials: $auth"; exit 1; }
    grep -qi '^Cache-Control:.*private.*no-store' "$work/headers" || { echo 'FAIL missing private/no-store'; exit 1; }
    if grep -qi '^CF-Cache-Status:.*HIT' "$work/headers"; then echo 'FAIL protected response served from Cloudflare cache'; exit 1; fi
    again=$(status "https://$host/")
    [ "$again" = 401 ] || { echo "FAIL AC-16 unauthenticated repeat: $again"; exit 1; }
    echo 'PASS AC-06/16/27 partial: correct password works and unauthenticated repeat is denied'
  else echo 'SKIP authorized/cache checks: provide generated test-site credentials in environment'; fi
  for addr in "$origin4" "$origin6"; do
    [ -n "$addr" ] || continue
    resolve=$addr; [[ "$addr" == *:* ]] && resolve="[$addr]"
    # The origin certificate is not browser-trusted. -k isolates the client-cert
    # check here; normal proxied checks above still validate the server cert.
    direct=$(status -k --resolve "$host:443:$resolve" "https://$host/" || true)
    case "$direct" in 400|403|421) echo "PASS AC-26 partial: origin $addr rejects request without zone certificate";;
      000) echo "MANUAL AC-26: origin $addr is unreachable; cannot establish certificate enforcement";;
      *) echo "FAIL origin $addr unexpected status $direct"; exit 1;;
    esac
  done
else echo 'SKIP site checks: set REPORT_HOSTING_SITE_HOST to an existing deployed test site'; fi
echo 'MANUAL: complete RUNBOOK live checklist for lifecycle, cross-site auth, uploads, limits, retention, sessions, audit, invalid Access JWT, shared/foreign AOP certificates, IPv6 and unchanged visionp.uk services.'
echo 'This script does not establish full AC-01 through AC-29 acceptance.'
