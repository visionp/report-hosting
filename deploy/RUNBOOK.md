# Deployment and operations

Status: prepared; **not executed on the production VPS**. Record the date,
operator and evidence next to each checkbox when performed. Leave an item
unchecked until the expected result is observed. Any failed item blocks launch.

## Before changes

- [ ] Record the distribution, Nginx version (`nginx -V`), filesystem and free
  space (`df -h /var/lib`). Nginx must support TLS, Basic Auth and real_ip.
- [ ] Save `sudo nginx -T` privately, and export the `visionp.uk` DNS zone from
  Cloudflare > DNS > Records > Import and Export. Store the export outside the
  repository. Record working URLs for existing services and their HTTP results.
- [ ] Identify the current default virtual host for every IPv4/IPv6 listener.
  If a listener uses an implicit default (its first loaded server), choose the
  new include location/order so that the existing first server remains first.
  Installing a new file earlier can change an implicit default even without a
  `default_server` directive. This is a pre-install gate; do not change existing
  service blocks to work around it.
- [ ] Run the Docker `test` target or all README checks. A missing dependency or
  skipped integration test is not sufficient evidence.

## Cloudflare and certificates

- [ ] Add `partnersflex.space` as its own Cloudflare zone. Change only this
  domain's registrar nameservers. Overview must report Active.
- [ ] DNS > Records: create proxied wildcard A record `*` targeting the VPS IP.
  Add AAAA only if that origin IPv6 address is configured and tested. DNS records
  in `visionp.uk` must be unchanged.
- [ ] SSL/TLS > Overview: select Full (strict). SSL/TLS > Origin Server: create
  an Origin Certificate for `partnersflex.space` and `*.partnersflex.space`.
  Install the certificate and private key at the paths in `nginx/tls.conf`.
- [ ] Configure zone-level Authenticated Origin Pulls using a custom client
  certificate for this zone. Install its issuing CA as
  `/etc/report_hosting/tls/zone-client-ca.pem`. Confirm the setup is supported by
  the account. Do not substitute Cloudflare's shared AOP certificate.
- [ ] Rules > Cache Rules: create a Bypass cache rule covering every hosted
  `*.partnersflex.space` hostname. Confirm rule order does not override bypass.
- [ ] Zero Trust > Access > Applications: create a self-hosted application for
  `admin.partnersflex.space`. Permit the intended administrator identity only.
  Copy its AUD and team hostname into configuration. Access verification remains
  enabled in the Go application.

Reference: [Cloudflare AOP setup](https://developers.cloudflare.com/ssl/origin-configuration/authenticated-origin-pull/).
Cloudflare console labels may vary; verify the resulting policy rather than
assuming a saved form proves enforcement.

## VPS installation

The commands below target Debian/Ubuntu with Nginx workers running as `www-data`.
Check the actual worker account before granting group membership. Run the
installation as a privileged operator. Run the application as `report_hosting`.

```sh
sudo useradd --system --user-group --home-dir /var/lib/report_hosting --shell /usr/sbin/nologin report_hosting
sudo usermod -aG report_hosting www-data
sudo install -d -o report_hosting -g report_hosting -m 0750 /var/lib/report_hosting
sudo install -d -o report_hosting -g report_hosting -m 0750 /var/lib/report_hosting/sites /var/lib/report_hosting/by-slug
sudo install -d -o report_hosting -g report_hosting -m 0700 /var/lib/report_hosting/db /var/lib/report_hosting/staging
sudo install -d -o root -g report_hosting -m 0750 /etc/report_hosting
sudo install -d -o root -g root -m 0700 /etc/report_hosting/tls
sudo install -d -o root -g root -m 0755 /etc/nginx/report_hosting.d
sudo install -m 0755 out/report_hosting /usr/local/bin/report_hosting
sudo install -o root -g report_hosting -m 0640 deploy/config.example.json /etc/report_hosting/config.json
sudo install -o root -g root -m 0600 /dev/null /etc/report_hosting/report_hosting.env
sudo install -m 0644 deploy/nginx/tls.conf deploy/nginx/cloudflare-realip.conf /etc/nginx/report_hosting.d/
sudo install -m 0644 deploy/nginx/report_hosting.conf /etc/nginx/conf.d/report_hosting.conf
sudo install -m 0644 deploy/systemd/report_hosting.service /etc/systemd/system/report_hosting.service
```

If the service user already exists, inspect its account and memberships instead
of repeating `useradd`. Install the three certificate files before validating
Nginx; keep private keys root-owned, mode 0600. Edit the example configuration to
set actual Access settings. The environment file may remain empty when all
configuration is in JSON.

- [ ] Confirm no other service account was added to the `report_hosting` group.
  `www-data` must read released files and `.htpasswd`, and must not read `db/`
  or `staging/`.
- [ ] Run the following, expecting valid configuration, administrator created,
  successful Nginx validation and active systemd service:

```sh
sudo -u report_hosting /usr/local/bin/report_hosting check-config -config /etc/report_hosting/config.json
sudo -u report_hosting /usr/local/bin/report_hosting admin create -config /etc/report_hosting/config.json administrator
sudo nginx -t
sudo systemctl daemon-reload
sudo systemctl enable --now report_hosting
sudo systemctl reload nginx
sudo systemctl is-active report_hosting
sudo journalctl -u report_hosting -n 50 --no-pager
```

The new Nginx file intentionally has no `default_server` directive: that setting
belongs to an address/port, not a domain, and could affect existing services.
It catches project names with exact and ordered regex server names. Audit
`nginx -T` for conflicting exact/wildcard server names before enabling it. The
SNI/Host equality and certificate-result guards apply to these project blocks.
They cannot impose TLS policy on unrelated virtual hosts. Origin verification
and the application's Cloudflare Access JWT verification are both required.
See [Nginx server names](https://nginx.org/en/docs/http/server_names.html).

The missing-site guard tests the full `$document_root`, including `current`.
Testing the shorter `by-slug/{slug}` path conflicts with
`disable_symlinks on from=$document_root`: on shorter paths Nginx would reject
the service-managed link itself. The full-root check still returns 404 before
Basic Auth when an unknown, disabled or not-yet-published site has no root.
Links inside a published release remain prohibited and are integration-tested.

Refresh the trusted Cloudflare ranges against [IPv4](https://www.cloudflare.com/ips-v4)
and [IPv6](https://www.cloudflare.com/ips-v6), then validate and reload Nginx.
Do not trust forwarded visitor addresses from arbitrary source networks.

## Live acceptance

- [ ] Without a Cloudflare Access session, the admin host requires Access login.
  With Access, the application still requires its own login. Invalid/expired
  Access assertions are rejected by the application.
- [ ] Create two sites. Save generated credentials privately. Upload both a ZIP
  and a single HTML file. Check correct credentials return content, wrong
  credentials and credentials from the other site return 401.
- [ ] Immediately repeat an authorized request without credentials, including
  requests for `.css`, `.js` and image paths. Each protected path must return 401;
  no Cloudflare cache may serve the earlier content. Check `private, no-store`.
- [ ] Upload invalid ZIPs and exceed each configured limit. The previous version
  remains active. A newly created release must be readable by Nginx, not 403.
- [ ] Rename a site, disable it, rename while disabled, restart Go, then enable
  it. Disabled names must remain unavailable after restart; only the final name
  works after enabling. No Nginx reload occurs during these operations.
- [ ] Regenerate credentials; the old password fails. Delete a disabled test
  site with exact slug confirmation; its files and routing link disappear.
- [ ] After five deployments, verify only three releases remain. Check audit
  events for all mutations and verify logs contain no passwords, session tokens,
  Authorization or Cloudflare identity headers.
- [ ] Test origin connections on both actual public IPv4 and IPv6 addresses
  using `curl --resolve` for each project hostname. Without the custom client
  certificate, no protected content is returned. Repeat with a certificate from
  an unrelated CA and with the shared Cloudflare AOP certificate; both fail.
  Test mismatched SNI/Host against the origin as well. Local self-signed tests
  cannot establish that the actual Cloudflare zone is correctly configured.
- [ ] Admin responses carry `frame-ancestors 'none'` and `X-Frame-Options: DENY`.
  Sessions expire after 30 minutes idle and 12 hours absolute duration.
- [ ] Compare the before/after `visionp.uk` zone exports (`diff -u before after`).
  There must be no project-caused record change. Repeat baseline requests to all
  existing services; compare the relevant Nginx blocks with the saved snapshot.

## Routine operations and recovery

For upgrades through GitHub Actions, complete the one-time setup in
[GITHUB_ACTIONS.md](GITHUB_ACTIONS.md). The updater checks the verified artifact,
atomically replaces the executable, restarts the service and checks its HTTP
response. It creates no backups and performs no automatic rollback. It does not
install or reload Nginx.

Use `systemctl status report_hosting`, `journalctl -u report_hosting` and `df -h`
to inspect failures. The service runs recovery before accepting HTTP traffic:
`current` selects the published version; database metadata is aligned with it,
unused ready releases are removed, and active routing links are rebuilt.

If startup reports an invalid or missing release target, keep the service
stopped and inspect the site directory and database together. Do not manually
move symlinks between sites or delete active releases. Repair requires evidence
of the intended site/release ownership.

For a binary upgrade, install the new binary, restart the service and repeat
smoke tests. If deployment fails, inspect the service log and deploy a corrected
build. Release restore is not implemented.

Backups and alerting are deferred and are not part of the deployment workflow.
