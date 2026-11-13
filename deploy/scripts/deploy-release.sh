#!/usr/bin/env bash
# Install this helper as root-owned /usr/local/sbin/report-hosting-deploy.
# Only the application binary is updated; Nginx, systemd and secrets are managed
# explicitly by the operator. No backups or automatic rollback are performed.
set -Eeuo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
umask 077

fail() { echo "ERROR: $*" >&2; exit 1; }
[[ $(id -u) == 0 ]] || fail 'Run the installed helper using sudo.'
[[ $# == 1 && $1 =~ ^[0-9a-f]{40}-[0-9]+-[0-9]+$ ]] || fail 'Expected COMMIT_SHA-RUN_ID-RUN_ATTEMPT.'
release=$1
incoming=/opt/report-hosting/releases/$release
state=/var/lib/report_hosting-deploy
binary=/usr/local/bin/report_hosting
config=/etc/report_hosting/config.json
envfile=/etc/report_hosting/report_hosting.env
service=report_hosting.service
health_url=http://127.0.0.1:8080/login

for tool in flock sha256sum systemctl systemd-run curl install cmp python3; do
  command -v "$tool" >/dev/null || fail "Missing command: $tool"
done
[[ -d $state && ! -L $state && $(stat -c '%u:%a' "$state") == 0:700 ]] || fail 'Create the root-owned deployment state directory (mode 0700); see deploy/GITHUB_ACTIONS.md.'
exec 9>"$state/deploy.lock"
flock -n 9 || fail 'Another report-hosting deployment is running.'
[[ -s $config && -f $envfile ]] || fail 'Complete the production configuration first.'
systemctl cat "$service" >/dev/null || fail 'Install the report_hosting systemd unit first.'

# Copy fixed filenames into root-owned staging before verifying them. The SSH
# account can write incoming artifacts, but cannot replace a checked candidate.
stage=$(mktemp -d "$state/candidate.XXXXXX")
cleanup() {
  local result=$?
  trap - EXIT INT TERM
  set +e
  if (( result != 0 )); then
    echo 'Deployment failed. Inspect journalctl -u report_hosting and deploy a corrected build.' >&2
  fi
  rm -f "$binary.next"
  rm -rf -- "$stage"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

healthy() {
  local code
  for _ in {1..20}; do
    # A live protected application rejects a request without an Access JWT.
    # Check the body too, so an unrelated listener returning 403 cannot pass.
    if systemctl is-active --quiet "$service"; then
      code=$(curl --noproxy '*' -sS --connect-timeout 1 --max-time 2 \
        -H 'Host: admin.partnersflex.space' -o "$stage/health.body" \
        -w '%{http_code}' "$health_url" 2>/dev/null) || code=000
      if [[ $code == 403 ]] && [[ $(cat "$stage/health.body") == 'access denied' ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}

for file in report_hosting SHA256SUMS; do
  [[ -f $incoming/$file && ! -L $incoming/$file ]] || fail "Missing regular artifact: $file"
  install -o root -g root -m 0600 "$incoming/$file" "$stage/$file"
done
[[ $(wc -l < "$stage/SHA256SUMS") == 1 ]] || fail 'Invalid checksum manifest.'
grep -Eq '^[0-9a-f]{64}  report_hosting$' "$stage/SHA256SUMS" || fail 'Missing binary checksum.'
(cd "$stage" && sha256sum --check --strict SHA256SUMS)

# Keep staging private. Put the candidate beside the executable for validation
# under the actual service identity and the same systemd EnvironmentFile.
install -o root -g root -m 0755 "$stage/report_hosting" "$binary.next"
systemd-run --quiet --wait --pipe --collect --unit=report-hosting-config-check \
  --uid=report_hosting --gid=report_hosting \
  --property="EnvironmentFile=$envfile" --property=RuntimeMaxSec=30s --property=NoNewPrivileges=yes \
  "$binary.next" check-config -config "$config"
# Verify the deployment profile and HTTP listener, including
# EnvironmentFile overrides. Do not source that file as a root shell script.
systemd-run --quiet --wait --pipe --collect --unit=report-hosting-profile-check \
  --uid=report_hosting --gid=report_hosting \
  --property="EnvironmentFile=$envfile" --property=RuntimeMaxSec=30s --property=NoNewPrivileges=yes \
  python3 - "$config" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as config_file:
    config = json.load(config_file)
expected = {"data_dir": "/var/lib/report_hosting", "listen_addr": "127.0.0.1:8080",
            "production": True, "access_check": True, "base_domain": "partnersflex.space"}
for key, wanted in expected.items():
    value = os.environ.get("REPORT_HOSTING_" + key.upper(), config.get(key, wanted))
    if isinstance(wanted, bool) and isinstance(value, str):
        value = value in ("1", "t", "T", "TRUE", "true", "True")
    if value != wanted:
        sys.exit("Unsupported deployment profile setting: " + key)
for key in ("access_team_domain", "access_aud"):
    value = os.environ.get("REPORT_HOSTING_" + key.upper(), config.get(key, ""))
    if not value or "REPLACE" in value:
        sys.exit("Configure the real Cloudflare Access setting: " + key)
PY

# Replace the executable atomically, then restart only this application.
mv -f "$binary.next" "$binary"
systemctl restart "$service"
healthy || fail 'The new application did not pass the protected HTTP check.'
# Ensure the responding service is actually running the installed executable.
pid=$(systemctl show --property=MainPID --value "$service")
[[ $pid =~ ^[1-9][0-9]*$ ]] || fail 'No application MainPID.'
cmp -s "$binary" "/proc/$pid/exe" || fail 'The service is not running the installed executable.'
printf '%s\n' "$release" > "$state/current-release.next"
mv -f "$state/current-release.next" "$state/current-release"
echo "Deployed $release."
