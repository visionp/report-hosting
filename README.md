# report_hosting

Private static-site hosting on one Linux VPS. One administrator publishes ZIP
archives or single HTML files at `{slug}.partnersflex.space`. Nginx serves each
site with separate Basic Auth credentials. The Go application manages metadata,
uploads, releases, access and the administration panel.

## Build and verify

Go 1.26 or later is required. The target operating system is Linux. SQLite uses
the pure-Go modernc driver; the release binary does not require CGO.

```sh
go test ./...
go vet ./...
go test -race -count=1 ./...
go build -o report_hosting ./cmd/report_hosting
bash deploy/scripts/nginx-itest.sh
```

The Nginx test starts an isolated local instance and needs Nginx, OpenSSL and
curl. It prints `SKIP` if dependencies are absent. A skip is not a production
acceptance result. Linux is required to verify Unix file permissions and the
symbolic-link behavior used in production.

Docker can supply the Linux build and verification environment on Windows:

```sh
docker build --target test -t report-hosting-test .
docker build --target binary --output type=local,dest=out .
```

The second command exports `out/report_hosting`. Production uses the included
systemd unit on the VPS; the Dockerfile is a build and test tool.

After exporting the binary, verify its real HTTP lifecycle in an isolated
container (temporary test data is removed on exit):

```sh
docker run --rm --mount type=bind,source="$(pwd)",target=/src,readonly report-hosting-test bash /src/deploy/scripts/app-smoke.sh /src/out/report_hosting
```

On PowerShell, replace `$(pwd)` with the absolute project folder path.

## Configuration and operation

Copy `deploy/config.example.json` to `/etc/report_hosting/config.json`. Replace
the Cloudflare Access team hostname and application audience before production.
Configuration precedence is defaults, JSON file, then `REPORT_HOSTING_*`
environment overrides. Unknown JSON properties and invalid settings are rejected.

| Setting | Default / purpose |
| --- | --- |
| `data_dir` | `/var/lib/report_hosting` |
| `listen_addr` | `127.0.0.1:8080`; loopback addresses only |
| `base_domain` | `partnersflex.space` |
| `production`, `access_check` | Both true; production cannot disable Access |
| `access_team_domain`, `access_aud` | Required when Access verification is enabled |
| `limits.max_zip_bytes` | 52,428,800 bytes |
| `limits.max_extracted_bytes` | 262,144,000 bytes |
| `limits.max_files`, `limits.max_ratio` | 5,000 files; 100:1 expansion |
| `reserved_slugs` | admin, www, api, mail, ftp, status, cdn, static, ns1, ns2 |
| `argon2.memory`, `iterations`, `parallelism` | 65,536 KiB, 3, 2 |

Environment overrides: `REPORT_HOSTING_DATA_DIR`, `LISTEN_ADDR`, `BASE_DOMAIN`,
`PRODUCTION`, `ACCESS_CHECK`, `ACCESS_TEAM_DOMAIN`, `ACCESS_AUD` (all with the
`REPORT_HOSTING_` prefix). Restart the process after changes.

```sh
report_hosting check-config -config /etc/report_hosting/config.json
report_hosting admin create -config /etc/report_hosting/config.json administrator
report_hosting serve -config /etc/report_hosting/config.json
```

Administrator creation reads the password without echo from a terminal, or from
standard input when piped. Never put passwords in command arguments. It refuses
to create a second administrator. Failed commands return a nonzero exit status.

For explicit local development, use `production: false` and `access_check: false`
in a separate config file. The browser-facing service still needs HTTPS for its
Secure session cookie; use a local TLS reverse proxy. This configuration must
never be installed on the public VPS.

## Storage and safety

Metadata lives in `db/report_hosting.sqlite`; releases live under
`sites/{uuid}/releases/{version}`. The `current` symbolic link selects the active
release. `by-slug/{slug}` points to a site, and removal of that link disables
public access. Keep staging and releases on the same filesystem.

Generated passwords are shown only in the create/regenerate result. They cannot
be retrieved later. `.htpasswd` holds a salted SSHA hash accepted by Nginx; the
database holds no site password. The application keeps the active release and
two older releases. Permanent deletion requires a disabled site and exact slug
confirmation. Backups, restore UI and alert delivery are outside the MVP.

Follow [the deployment runbook](deploy/RUNBOOK.md) for Cloudflare, Nginx, systemd,
production checks and recovery. The [specification](report_hosting_technical_specification.md)
and [implementation plan](docs/superpowers/plans/2026-11-13-report-hosting-mvp.md)
define acceptance. A passing local build does not confirm live DNS, Cloudflare
policy, IPv6 access controls or preservation of existing VPS services.
