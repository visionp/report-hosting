# GitHub Actions deployment on the shared VPS

The repository is `visionp/report-hosting`. A push to `main`, or a manual run of
**Deploy** on `main`, calls **Checks** before deploying. Pull requests and a manual
run of **Checks** only verify and package the application.

## Existing VPS conventions

Inspected from the neighboring repositories; these are configured values, not a
live inventory of listening sockets:

| Project | Production directory | Host loopback ports | Runtime |
| --- | --- | --- | --- |
| fin-admin | `/opt/fin-admin` | 3001, 8081 | Docker Compose |
| flex_crm | `/opt/flex_crm` | 8000 | Docker Compose |
| flex_report | `/opt/flex-report` | 5190 | Docker Compose |
| flex_accounts | `/opt/flex-accounts` | 5191 | Docker Compose |
| report-hosting | `/opt/report-hosting/releases` | 8080 | Go binary + systemd |

This project uses the same SSH secret names and the reusable checks/deploy
pattern. Its existing systemd architecture is retained. Go and Docker are needed
on the GitHub runner, not on the production VPS. The runner is Linux x86-64;
confirm `uname -m` is `x86_64` on the VPS before enabling deployment.

## Repository secrets

In GitHub: **visionp/report-hosting > Settings > Secrets and variables > Actions**.
Secrets from other repositories are not automatically shared. Either add them
here or grant this repository access to organization secrets.

| Secret | Value |
| --- | --- |
| `SERVER_HOST` | The same VPS address used by the neighboring projects |
| `SERVER_USER` | The existing SSH deploy account, with the permissions below |
| `SERVER_SSH_KEY` | That account's private SSH key; never commit it |
| `SERVER_FINGERPRINT` | Trusted SHA256 fingerprint of the VPS SSH host key |

SSH uses port 22. Obtain the fingerprint through the VPS console or an already
trusted SSH session, for example `sudo ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256`.
Use its `SHA256:...` field. Both file transfer and remote execution verify it.

## One-time VPS preparation

Complete [RUNBOOK.md](RUNBOOK.md) first: real Cloudflare Access settings, TLS and
zone client certificates, administrator, application account, Nginx and systemd.
Do not enable public access using the example `REPLACE...` values. Check actual
port usage with `sudo ss -ltnp` and preserve existing Nginx default virtual hosts.

The updater requires the application account, production configuration and
systemd unit to be installed. It also works when the service is inactive after a
failed deployment. It does not provision DNS, certificates, the administrator,
or Nginx from repository defaults. For the first executable, run **Checks** and download its
`report-hosting-<commit>` artifact, or build that exact revision locally:

```sh
docker build --target test -t report-hosting-test .
docker build --target binary --output type=local,dest=out .
(cd out && sha256sum report_hosting > SHA256SUMS)
```

Use the `out/report_hosting` executable in RUNBOOK's installation commands.
When using a GitHub artifact, unzip it into `out/`; the archive contains the
binary and `SHA256SUMS` at its root.
Check them with `(cd out && sha256sum --check --strict SHA256SUMS)`.

Run the following as the VPS operator from a copy of this repository and its
matching `out/` artifacts. Replace `deploy` with the actual `SERVER_USER`:

```sh
deploy_user=deploy
id "$deploy_user"
sudo apt-get update
sudo apt-get install -y curl python3 util-linux
sudo install -d -o root -g root -m 0755 /opt/report-hosting
sudo install -d -o "$deploy_user" -g "$(id -gn "$deploy_user")" -m 0750 /opt/report-hosting/releases
sudo install -d -o root -g root -m 0700 /var/lib/report_hosting-deploy
sudo install -o root -g root -m 0755 deploy/scripts/deploy-release.sh /usr/local/sbin/report-hosting-deploy
sudo systemctl is-active report_hosting.service
```

Create `/etc/sudoers.d/report-hosting-deploy` with `sudo visudo -f` and the actual
deploy username. The rule permits this root-owned helper only:

```sudoers
deploy ALL=(root) NOPASSWD: /usr/local/sbin/report-hosting-deploy
```

```sh
sudo chmod 0440 /etc/sudoers.d/report-hosting-deploy
sudo visudo -cf /etc/sudoers.d/report-hosting-deploy
```

The deploy account must not own the helper, systemd unit, `/usr/local/bin`,
`/etc/report_hosting`, or deployment state directory. Deployment grants control
of the application executable running as `report_hosting`; protect pushes to
`main` and workflow changes accordingly. No root shell or arbitrary systemctl
permission is needed. A private repository checkout and GitHub deploy key are
not needed on the VPS: Actions uploads the verified binary directly.

The helper expects the standard unit and production profile: data under
`/var/lib/report_hosting`, `127.0.0.1:8080`, `partnersflex.space`, and both
`production` and `access_check` true. It checks JSON and EnvironmentFile
overrides using the service identity. Access team/AUD may be supplied through
the environment file. Keep the installed unit consistent with
`deploy/systemd/report_hosting.service`; do not override its user or ExecStart.

## What each deployment does

1. Run Go vet/race tests, deployment rehearsals, shell lint and Nginx integration
   in the Docker test target. A failed check blocks transfer.
2. Build a Linux binary from the same checkout, then test its HTTP lifecycle
   against an isolated synthetic database. Upload it as a GitHub artifact.
3. Transfer only the binary and its checksum into a unique
   `/opt/report-hosting/releases/<commit>-<run>-<attempt>/` directory.
4. Acquire a server lock and copy the candidate into root-owned staging. Verify
   the checksum and production configuration.
5. Atomically replace the executable and restart only `report_hosting.service`.
6. Require a protected HTTP response and confirm MainPID runs the installed
   executable. Record the successful release. A restart causes brief admin
   downtime; Nginx continues serving already published reports.

No deployment step reloads Nginx, changes DNS, rewrites production configuration,
or restarts neighboring services. Update root-owned deployment helpers, units
and Nginx snippets explicitly after reviewing their diffs. The existing helper
does not silently replace itself when repository scripts change.

The deployment does not copy the database, hosted files or previous executable.
Backups and automatic rollback are not implemented. If restart or health
verification fails, the workflow fails without recording a successful release.
Inspect the service log, fix the problem and deploy again. The service does not
have to be healthy for that retry. Database initialization/migrations remain
part of the application's existing startup process.

## Verification

After the first automated run, verify the Actions log and on the VPS:

```sh
sudo cat /var/lib/report_hosting-deploy/current-release
sudo systemctl is-active report_hosting
sudo journalctl -u report_hosting -n 50 --no-pager
curl --noproxy '*' -i -H 'Host: admin.partnersflex.space' http://127.0.0.1:8080/login
```

The local request must return `403` with `access denied` because it has no Access
assertion. This verifies startup and protection, not valid administrator login,
DNS or TLS. Complete RUNBOOK's live acceptance through Cloudflare and check the
existing services after the first installation.

The root-only `/var/lib/report_hosting-deploy/` directory holds the deployment
lock, temporary staging files and successful release identifier. Staging files
are removed on completion. Incoming build artifacts can be removed after a
successful deployment when no run is using them.

Local Linux verification:

```sh
docker build --target test -t report-hosting-test .
docker build --target binary --output type=local,dest=out .
docker run --rm --mount type=bind,source="$(pwd)",target=/src,readonly report-hosting-test bash /src/deploy/scripts/app-smoke.sh /src/out/report_hosting
```

The deployment rehearsal uses temporary synthetic files and mocked systemd/HTTP
commands. It verifies success without copying application data or the old
executable, corrupted artifacts, profile rejection, failed health checks,
retries, path validation and concurrent deploys.
Actual systemd permissions and SSH access still need verification on the VPS.
