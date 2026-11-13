# VISIONP

## Static Website Hosting and Administration Service

*Requirements and proposed technical solution*

| Document status | Proposed (design decisions confirmed) |
| --- | --- |
| Version | 1.2 |
| Date | 13 November 2026 |
| Product name | visionp |
| Domain namespace | partnersflex.space |
| Target platform | Debian or Ubuntu VPS |
| Backend language | Go |

> **Purpose**
> This document defines the problem, the system requirements, and the proposed solution for visionp. The service will host up to 20 static websites on the existing VPS. It will use the dedicated domain partnersflex.space. It will use the current VPS, Nginx, Go, SQLite, and Cloudflare.

> **Writing note.** This document uses short sentences, consistent terms, and direct instructions. It follows the writing principles of ASD-STE100. It is not a certified STE document. A formal compliance review requires the current standard and an STE checker.

> **Change from version 1.0.** The service now uses the dedicated domain partnersflex.space instead of report.visionp.uk. The visionp.uk DNS zone is not changed. Site routing uses one Nginx rule and symbolic links. It does not use a generated map and a root helper. Backups and alerting are outside the MVP. See section 16.2. Version 1.2 adds the security and consistency fixes from a design review.

# 1. Executive Summary

visionp is a small website hosting service. It will run on the existing VPS. An administrator will use a web panel to create a site, upload its files, regenerate its access password, and manage its address.

The service will support two upload types:

- A ZIP archive that contains a static website.
- A single HTML file. The service will publish this file as index.html.

Nginx will serve website files. The Go application will manage sites and deployments. SQLite will store site metadata. The file system will store website releases. Cloudflare will provide DNS proxying and TLS at the edge.

The service will use these host names:

| Purpose | Host name |
| --- | --- |
| Admin panel | admin.partnersflex.space |
| Hosted website | {slug}.partnersflex.space |
| Base domain | partnersflex.space |
| Wildcard DNS and certificate name | *.partnersflex.space |

> **Main design decision**
> Use one VPS and one service stack. Do not use Docker for the MVP. Do not change any DNS record in the visionp.uk zone. Other visionp.uk services use the same VPS and Cloudflare proxy. This project must not affect them. Use Nginx for static traffic and Go for administration.

# 2. Problem Statement

The project needs a simple way to publish and manage static websites. The current VPS must host these websites without a separate server for each site.

The administrator needs one place to perform these tasks:

- Create a website and assign a unique subdomain.
- Upload a ZIP archive or one HTML file.
- Regenerate the website password.
- Publish a new version without a long service outage.
- View the site address and deployment status.
- Disable, enable, and delete a website.

The system must keep hosted websites separate from the admin panel. It must also avoid any change to other services that use visionp.uk subdomains.

# 3. Goals and Out of Scope

## 3.1 Goals

- Host up to 20 static websites on one existing VPS.
- Use Go for the backend service.
- Support ZIP and single-HTML uploads.
- Use a unique subdomain for each website.
- Protect each website with its own HTTP Basic Authentication credentials.
- Use a simple admin panel with no separate JavaScript build system.
- Limit new costs to the domain partnersflex.space. Use the existing VPS and the free Cloudflare plan.
- Use safe and repeatable deployment steps.
- Make no change to the visionp.uk DNS zone, certificates, or Nginx virtual hosts.

## 3.2 Out of Scope for the MVP

- Dynamic application hosting, PHP, Node.js, or server-side website code.
- User registration or public account creation.
- Multiple administrators with role-based access control.
- Custom domains outside partnersflex.space.
- Automated Git deployment or CI/CD integration.
- An object storage service or a separate hosting cluster.
- A visual website builder or an online HTML editor.
- Backups and restore (deferred; see section 12.3).
- Monitoring dashboards and alert delivery (deferred; see section 12.4).
- Restore of a prior release.

# 4. Scope and System Boundaries

## 4.1 Constraints

| Item | Decision |
| --- | --- |
| Operating system | Debian or Ubuntu on the existing VPS. |
| Backend | Go. |
| HTTP server | Nginx 1.18 or later. Allow TLS 1.2 and TLS 1.3 only. |
| Metadata store | SQLite. |
| File storage | Local VPS file system. |
| DNS and edge TLS | Cloudflare (free plan, Universal SSL). |
| Service manager | systemd. |
| Expected site count | Up to 20 sites. Traffic volume is not defined; confirm VPS capacity before launch. |
| Deployment model | One VPS. No Docker in the MVP. |
| Shared VPS | Other services use the same VPS and Nginx. Add new server blocks only. Do not edit existing ones. |

## 4.2 Domain Rules

Use only the partnersflex.space domain for this project. Do not add or change records in the visionp.uk zone.

```text
Admin:       admin.partnersflex.space
Website:     {slug}.partnersflex.space
Wildcard:    *.partnersflex.space
```

The slug must contain 1 to 32 lower-case letters, digits, or hyphens. The slug must start and end with a letter or digit. A hyphen must not appear twice in a row. Reserve these slugs: admin, www, api, mail, ftp, status, cdn, static, ns1, ns2. Make the list configurable.

Each website must use one subdomain label. Do not support nested names such as shop.demo.partnersflex.space in the MVP.

# 5. Functional Requirements

The words MUST, SHOULD, and MAY have the following meaning in this document: MUST is required for acceptance. SHOULD is recommended. MAY is optional.

| ID | Requirement |
| --- | --- |
| FR-01 | The administrator MUST be able to sign in to the admin panel. |
| FR-02 | The administrator MUST be able to create a site with a name and a unique slug. |
| FR-03 | The system MUST reject an invalid, reserved, or duplicate slug. |
| FR-04 | The administrator MUST be able to upload a ZIP archive. |
| FR-05 | The administrator MUST be able to upload one HTML file. The system MUST publish it as index.html. |
| FR-06 | The system MUST validate an archive before it publishes any file. |
| FR-07 | The system MUST require index.html at the archive root. The system MUST also accept an archive with exactly one top-level directory that contains index.html. In that case, the directory becomes the site root. The system MUST ignore __MACOSX entries and .DS_Store files. The system MUST reject all other layouts. |
| FR-08 | The system MUST generate a random password of at least 20 characters when it creates a site. The system MUST show the password once. The administrator MUST be able to regenerate the password and to change the site username. The username MUST contain only lower-case letters, digits, and hyphens (1 to 32 characters). The system MUST reject all other characters. |
| FR-09 | Each site MUST have separate HTTP Basic Authentication credentials. A site MUST NOT exist without credentials. |
| FR-10 | The system MUST publish a new release by an atomic switch to the current release. |
| FR-11 | The administrator MUST be able to view the site name, slug, URL, status, and last deployment time. |
| FR-12 | The administrator MUST be able to change a site slug. The system MUST update the route in the order of section 8.4: create the new link, update the database, then remove the old link. A short time in which both names work is accepted. |
| FR-13 | The administrator MUST be able to disable a site. The administrator MUST be able to enable a disabled site. The administrator MUST be able to delete a disabled site permanently, after the administrator types the site slug to confirm. |
| FR-14 | The system MUST return a controlled error for an unknown site host. |
| FR-15 | The system MUST keep the active release and the two previous releases for each site. The system MUST remove older releases after a successful deployment. A restore function MAY be added after the MVP. |

# 6. Proposed Architecture

## 6.1 Main Components

| Component | Responsibility |
| --- | --- |
| Cloudflare DNS and proxy | Resolve the project host names. Proxy HTTP traffic to the VPS. Provide edge TLS. |
| Cloudflare Access | Restrict access to admin.partnersflex.space before traffic reaches the VPS. |
| Nginx | Terminate origin TLS. Resolve the site from the host name. Enforce site Basic Auth. Serve static files. |
| Go application | Provide the admin UI and API. Validate uploads. Manage metadata, releases, and routing links. |
| SQLite | Store site data and release metadata. |
| Local file system | Store staged uploads, releases, routing links, and site password files. |
| systemd | Start, stop, and restart the Go application. |

## 6.2 Request Flow

```text
Administrator browser
        | HTTPS
        v
Cloudflare Access --> Cloudflare --> Nginx --> Go admin application
                                (admin.partnersflex.space)

Website visitor
        | HTTPS
        v
Cloudflare  --->  Nginx  --->  Basic Auth  --->  Static files
                         ({slug}.partnersflex.space)
```

Nginx must serve static files directly. Do not send normal website requests to the Go application. This reduces application load and keeps the request path simple.

## 6.3 Backend Technology

| Area | Proposed technology |
| --- | --- |
| HTTP server | Go standard library net/http. Use chi only if routing needs it. |
| Templates | Go html/template. |
| UI updates | HTMX. Store the HTMX file in web/static and serve it from the application. Do not load it from an external CDN. Avoid a separate Node.js build step. |
| Database | SQLite with modernc.org/sqlite. |
| Logging | log/slog to standard output and journald. |
| Password file | htpasswd file with {SSHA} hashes. See section 10. |
| Admin password hash | argon2id. |
| Service manager | systemd. |

# 7. Storage and Data Model

## 7.1 File Layout

```text
/var/lib/visionp/
  db/
    visionp.sqlite
  sites/
    {site-uuid}/
      current -> releases/000001
      .htpasswd
      releases/
        000001/
          index.html
          assets/
        000002/
          index.html
  by-slug/
    {slug} -> ../sites/{site-uuid}
  staging/
```

Use a UUID for each site directory. Do not use the slug as the storage key. This allows the administrator to change a slug without moving the site files.

The by-slug directory holds one symbolic link for each active site. Nginx uses it to find the site. Go creates, replaces, and removes these links. A disabled site has no link.

The current link points to the active release. Nginx reads files from this link. Keep .htpasswd outside the release directory. Do not expose it through a website URL. The staging directory and the sites directory MUST be on the same file system. This keeps renames atomic.

## 7.2 Database Tables

| Table | Main fields | Notes |
| --- | --- | --- |
| sites | id, name, slug, auth_username, status (active or disabled), current_release_id, created_at, updated_at | Store no plain-text password and no password hash. |
| releases | id, site_id, version, storage_path, checksum, status (ready or active), created_at | Use a unique version for each site. The status ready means that the release is on disk but is not yet active. Calculate the checksum with SHA-256. |
| audit_events | id, actor, action, site_id, created_at | Record admin actions. Required in the MVP. Do not store passwords or tokens. |
| admin_sessions | id_hash, created_at, last_seen_at, expires_at | Store server-side sessions. Store a hash of the session identifier. expires_at is the absolute end: created_at plus 12 hours. The 30-minute idle timeout counts from last_seen_at. |
| login_attempts | key, count, window_start, locked_until | Store failed-login counters for the lockout rule. |

Store the password hash in the site .htpasswd file. Limit read access to the service account and Nginx. Store the SQLite database in a directory that Nginx cannot serve. The database is the source of truth for sites and slugs. The service MUST be able to rebuild the by-slug links from the database. The only exception is the active release: the current symlink decides it (see the start recovery in section 8.2).

# 8. Upload and Deployment Process

## 8.1 ZIP Validation

The service MUST validate the full archive before it publishes files. Use safe extraction rules:

- Reject absolute paths and paths that contain a parent-directory segment such as ../.
- Reject paths that contain a null byte or a backslash.
- Reject symbolic links and special file types. Accept regular files and directories only.
- Reject duplicate paths, including paths that differ only by letter case. Reject attempts to replace platform-managed files.
- Ignore __MACOSX entries and .DS_Store files.
- Require index.html at the archive root, or inside exactly one top-level directory. Reject all other layouts with a clear error message.
- Limit the archive size, extracted size, file count, and compression ratio. Count the bytes that the service actually reads. Do not trust the sizes in the archive header.
- Do not extract files outside the staging directory.

Initial limits are 50 MB per ZIP, 250 MB of extracted data, 5,000 files, and a maximum expansion ratio of 100:1. Make these values configurable. The Cloudflare proxy limits a request body to 100 MB on the free plan. The 50 MB limit leaves a safe margin. Review the limits after the first use period.

Before each deployment, check the free disk space. Reject the upload if the free space is less than the extracted-size limit plus a safety margin.

## 8.2 Release Deployment

1. Receive the upload in a unique staging directory.
1. Validate the upload and extract it inside the staging directory. Do all checks here.
1. If the archive has exactly one top-level directory, select that directory as the site root. Do this before the index.html check.
1. Check that the site root contains a valid index.html.
1. Calculate the SHA-256 checksum.
1. Move the site root into the releases directory with one rename. The staging directory and the releases directory are on the same file system.
1. Record the release in SQLite with the status ready. The release is not active yet.
1. Create a temporary symlink to the new release. Atomically replace the current symlink with it.
1. In one SQLite transaction, set current_release_id and record the deployment result.
1. Remove temporary files. Remove releases older than the active release and the two previous releases.

At service start, run this recovery in this order:

1. For each site, compare the current symlink with current_release_id. If they differ, set current_release_id (and the release status active) to the release that the symlink points to.
1. Remove each release in the ready status that the current symlink does not point to. Remove its directory and its database row.
1. For each active site, create the by-slug link if it is missing.
1. Remove each by-slug link that has no matching active site in the database, or that points to a disabled site.

Do not reverse this order. A reverse order can return a published release to an older release or reopen a disabled site. Step 4 also removes the old link that a failed slug change leaves behind.

Allow only one deployment for each site at a time. Keep the previous release until the new release passes its checks. A failed upload or validation step MUST NOT change the active site.

## 8.3 Single HTML Upload

For a single HTML upload, save the uploaded file as index.html in a unique staging directory. Do not create a release directory at this step. Then continue the normal release deployment process from the step that checks index.html (section 8.2).

## 8.4 Slug Change

A slug change cannot be one atomic operation. Use this order: create the new by-slug link, update the database, then remove the old link. For a short time both names reach the site. Accept this window. Never remove the old link before the new link exists.

## 8.5 Credential File Update

The username and password hash go into the site .htpasswd file as one line: username:hash. Write the new file to a temporary file in the same directory. Then replace the old file with rename(). Nginx must never read a partial file.

# 9. Nginx and DNS Design

## 9.1 Cloudflare DNS

Add the domain partnersflex.space to Cloudflare as a new zone. Create one wildcard DNS record:

```text
Type:      A
Name:      *
Target:    <VPS_PUBLIC_IP>
Proxy:     Enabled
```

The wildcard record also covers admin.partnersflex.space. Do not add or change any record in the visionp.uk zone. Keep the Cloudflare proxy enabled for every name in this zone. A DNS-only record would expose the VPS address.

## 9.2 TLS

The free Cloudflare Universal SSL certificate covers partnersflex.space and *.partnersflex.space. Visitors receive this edge certificate.

Use a Cloudflare Origin Certificate for the connection from Cloudflare to Nginx. The certificate must cover partnersflex.space and *.partnersflex.space. Set Cloudflare SSL/TLS mode to Full (strict) for this zone. Do not use the Origin Certificate for direct browser access to the VPS.

The wildcard name *.partnersflex.space covers admin.partnersflex.space and demo.partnersflex.space. It does not cover a nested name such as shop.demo.partnersflex.space.

## 9.3 Nginx Virtual Hosts

Add new server blocks. Do not edit server blocks of other services. Use these blocks:

- An exact server_name for admin.partnersflex.space. Proxy requests to the Go application on a local address. Send the headers Content-Security-Policy: frame-ancestors 'none' and X-Frame-Options: DENY. A hosted site must not be able to show the admin panel in a frame.
- A regular-expression server_name for hosted websites. Serve files from the by-slug directory. Enforce Basic Auth.
- A default server for partnersflex.space hosts that rejects unknown host names.

The hosted-site block uses this rule:

```text
server_name ~^(?<slug>[a-z0-9-]+)\.partnersflex\.space$;
root                 /var/lib/visionp/by-slug/$slug/current;
auth_basic_user_file /var/lib/visionp/by-slug/$slug/.htpasswd;
disable_symlinks     on from=$document_root;
```

An exact server_name has priority over a regular expression. Because of this, admin.partnersflex.space always uses the admin block.

Add a check before authentication. If the directory /var/lib/visionp/by-slug/$slug does not exist, return 404. Without this check, an unknown or disabled site would cause an error for the missing password file. The slug comes from a strict character set. Do not build file paths from any other request data.

Nginx does not need a reload when a site is created, renamed, disabled, enabled, or deleted. Go changes only symbolic links and files. Nginx reads them on each request. The Go process must not write to /etc/nginx and must not run commands as root. Use a root-owned, manual process to install the static Nginx configuration. Validate it with nginx -t before each reload.

Protect the origin with zone-level Authenticated Origin Pulls that use a custom client certificate. Create the certificate for the partnersflex.space zone and upload it to Cloudflare. Do not use the shared Cloudflare certificate: it accepts requests from any Cloudflare account, so another customer could send traffic to the VPS and set the Host header to admin.partnersflex.space. In the new server blocks, set ssl_client_certificate to the CA of the custom certificate and ssl_verify_client to on. Nginx then rejects any connection that does not come from the partnersflex.space zone, on both IPv4 and IPv6 listeners. The application still validates the Cloudflare Access token, which does not depend on this control.

Use the real_ip module with set_real_ip_from for the Cloudflare IPv4 and IPv6 ranges and real_ip_header CF-Connecting-IP. Rate limits then see the visitor address. Apply these settings to the new server blocks only. Do not change the settings of other services.

## 9.4 Cache Policy

Disable Cloudflare caching for all hosts in partnersflex.space. Use two controls:

- A Cloudflare Cache Rule with the action Bypass cache for *.partnersflex.space.
- The header Cache-Control: private, no-store on every response from the hosted-site block.

Cloudflare caches common static file types by default. A cached response could reach a visitor without a Basic Auth check. Test that a repeated request without credentials returns 401.

# 10. Security Requirements

| Area | Requirement |
| --- | --- |
| Admin access | Use two layers. Cloudflare Access restricts access to the administrator identity. The application also requires an admin login with an argon2id password hash. Do not allow public registration. The application MUST validate the Cloudflare Access token in production: the signature (with Cloudflare public keys), the issuer, the audience, and the expiry. Provide a configuration switch to disable this check for local development only. The default value is that the check is on. The service MUST NOT start in production mode with the check off. |
| Admin login | Lock a login for 15 minutes after 5 failed attempts in 15 minutes. Count attempts for each source address and for the account. Store the counters in SQLite. Create the first administrator with a setup command (for example, visionp admin create). The command writes an argon2id hash with memory 64 MiB, 3 iterations, and parallelism 2. Make these values configurable. |
| Session cookies | Use Secure, HttpOnly, and SameSite=Strict cookies. Use the __Host- prefix, Path=/, and no Domain attribute. Do not set a parent-domain cookie. Keep sessions on the server in SQLite. Use an idle timeout of 30 minutes and an absolute lifetime of 12 hours. Issue a new session identifier after each successful login. Delete the session at logout. |
| Request validation | Validate Host and Origin. Add CSRF tokens to state-changing requests. Do not rely on SameSite alone, because hosted sites and the admin panel are same-site. Do not enable wildcard CORS. Forbid framing of the admin panel (see section 9.3). A hosted site is same-site with the admin panel, so a site could otherwise show the panel in a frame and trick the administrator into a click. |
| Site access | Use separate Basic Auth credentials for each site. Generate each password with a cryptographic random source (Go crypto/rand). Use at least 20 characters from an alphabet of at least 64 symbols, for example A-Z, a-z, 0-9, hyphen, and underscore. Do not use a colon. This gives about 120 bits of entropy. Do not store plain-text passwords. |
| Password hashes | Nginx checks the password on every request. Use the {SSHA} format. The Nginx auth_basic documentation lists it. Do not use bcrypt: it depends on the crypt() function of the operating system. Generate the file with the Go service, and verify with an integration test that Nginx accepts the correct password and rejects a wrong one. A long random password makes a fast hash acceptable. If a person-chosen password is added later, change the scheme and raise the hash cost. |
| Site username | Allow only lower-case letters, digits, and hyphens. Reject colon, space, newline, and all other characters. Replace the .htpasswd file by writing a temporary file and by rename() (see section 8.5). |
| Site existence | An unknown slug returns 404. An existing site returns 401 before login. A visitor can therefore find existing slugs. This is accepted in the MVP. Do not treat a slug as a secret. |
| Upload isolation | Use a staging directory. Reject archive traversal, links, special files, and unsafe paths. |
| File access | Run Go as a dedicated service user. Give Nginx read-only access to released files. Keep secrets and the database outside the web root. |
| Dotfiles | Deny requests for hidden files. Do not serve .htpasswd, database files, logs, or generated files. |
| Rate limits | Apply the login lockout above. Limit uploads to 10 requests per minute for each source address with the Nginx limit_req module. Limit all requests to a hosted site to 30 requests per second for each source address, with a burst of 60. The limit_req module counts all requests, not only failed logins. Make the values configurable. A random 20-character password makes guessing impractical, so a separate limit for failed Basic Auth requests is not required. |
| Logs | Do not log passwords, session tokens, full Authorization headers, or the Cloudflare Access headers Cf-Access-Jwt-Assertion and Cf-Access-Authenticated-User-Email. |
| Site isolation | Use one subdomain per site. Keep admin cookies host-only with the __Host- prefix. Treat uploaded HTML and JavaScript as untrusted browser content. |
| Cache | Follow section 9.4. |

Important isolation note: Sites and the admin panel share the registrable domain partnersflex.space. A site can run arbitrary browser code. A site can set cookies for the whole domain partnersflex.space. The __Host- cookie prefix and strict CSRF checks reduce cookie and request risks. They do not give the same isolation as a separate registrable domain. The risk stays inside this domain. Other visionp.uk services are not affected. The administrator uploads all site content in the MVP. Use a separate domain for the admin panel if the service later hosts content from untrusted users.

# 11. Admin UI and API

## 11.1 Admin UI

Use server-rendered HTML templates. Use HTMX for small updates. The MVP UI should provide:

- A site list with name, slug, URL, status, and update time.
- A create-site form. The result page shows the generated password once.
- An upload form for ZIP or HTML files.
- A credentials form to change the username and to regenerate the password.
- A site detail page with the public URL and current release.
- Disable and enable actions.
- A delete action for a disabled site. The administrator types the site slug to confirm.

## 11.2 API Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | /api/sites | List sites. |
| POST | /api/sites | Create a site and generate credentials. |
| GET | /api/sites/{id} | Get site details. |
| PATCH | /api/sites/{id} | Change site name or slug. |
| POST | /api/sites/{id}/disable | Disable a site. |
| POST | /api/sites/{id}/enable | Enable a site. |
| DELETE | /api/sites/{id} | Delete a disabled site permanently. The request body MUST contain the exact slug. |
| POST | /api/sites/{id}/deploy | Upload and deploy a new release. |
| POST | /api/sites/{id}/credentials | Change the username or regenerate the password. |
| GET | /api/sites/{id}/releases | List releases. Optional for the MVP. |
| POST | /api/sites/{id}/restore | Restore a prior release. Later phase. |

All admin API routes must require an authenticated session. State-changing routes must use CSRF protection. Return clear validation errors. Do not expose server paths or internal error details to the browser.

The server MUST reject a delete request if the body does not contain the exact slug of the site. Do not rely on the browser to check it. A permanent delete runs in this order: remove the by-slug link, delete the database row, then delete the files.

# 12. Deployment and Operations

## 12.1 Service Accounts and Permissions

Run the Go application as a dedicated visionp user. Do not run it as root. Use a dedicated group to grant Nginx read access to released website files, the by-slug links, and the .htpasswd files. Give write access only to the paths that the Go service needs. Nginx needs read access to the sites directory, the by-slug links, and the .htpasswd files. Do not give the group access to db/ and staging/. Set db/ and staging/ to mode 0700 for the visionp user. Do not add other services on the VPS to the visionp group. The Go service creates release files. Use umask 027, or set explicit modes: 0750 for directories and 0640 for files. Without these modes, Nginx cannot read a new release and returns 403. Test this with a new release.

## 12.2 systemd

Run the application as a systemd service. Use a fixed working directory and an explicit configuration file. Enable automatic restart after a process failure. Send logs to journald. Set User=visionp, NoNewPrivileges=true, ProtectSystem=strict, ProtectHome=true, PrivateTmp=true, and ReadWritePaths=/var/lib/visionp. Keep secrets in an EnvironmentFile with mode 0600, owned by root. systemd reads it before it starts the service as the visionp user.

## 12.3 Backups (deferred)

Backups are outside the MVP. Until backups exist, the VPS holds the only copy of the sites, the database, and the passwords. Add backups before the service hosts important sites. When added, they must:

- Back up the SQLite database and active site releases.
- Use a consistent SQLite backup method.
- Copy at least one backup set to a different system.
- Include a tested restore process.

## 12.4 Monitoring (minimal)

- Send logs to journald. Log upload, validation, deployment, rename, disable, and delete events.
- Check free disk space before each deployment (see section 8.1).
- Check the Go service state and the Nginx state by hand.
- Alerting and uptime monitoring are deferred.

# 13. Repository Structure

```text
visionp/
  cmd/visionp/main.go
  internal/
    config/
    domain/
    handler/
    service/
    repository/
    storage/
    routing/
  web/
    templates/
    static/
  migrations/
    001_init.sql
  deploy/
    nginx/
    systemd/
    scripts/
  Makefile
  go.mod
  README.md
```

Keep business rules in service packages. Keep HTTP handlers thin. Keep the routing package (by-slug links) separate from site and upload logic. Store the static Nginx configuration in deploy/nginx. Add tests for archive validation and deployment before the admin UI is complete.

# 14. Implementation Plan

| Phase | Deliverables |
| --- | --- |
| Phase 1 - VPS and network | Add partnersflex.space to Cloudflare. Create the wildcard DNS record. Install the origin certificate. Add the new Nginx server blocks. Create storage paths and the systemd unit. Configure the Cloudflare Access application and the Cache Rule. Enable zone-level Authenticated Origin Pulls with a custom certificate and configure the Nginx client certificate check. |
| Phase 2 - Backend core | Create the Go application. Add SQLite migrations. Implement site CRUD, configuration loading, and filesystem storage. |
| Phase 3 - Upload and deployment | Add ZIP validation and extraction. Add single-HTML upload. Implement release creation, atomic activation, and release cleanup. |
| Phase 4 - Routing and access | Manage the by-slug links. Generate credentials and the .htpasswd files. Add the missing-site check. Test {SSHA} authentication against the target Nginx. |
| Phase 5 - Admin UI | Add login, site list, site creation, upload, credentials, status, disable, enable, and delete actions. |
| Phase 6 - Hardening | Add rate limits, audit logs, disk checks, cache tests, and security tests. |
| Post-MVP | Backups and restore, alerting, release restore. |

# 15. Acceptance Criteria

| ID | Acceptance test |
| --- | --- |
| AC-01 | The admin panel opens at https://admin.partnersflex.space only after Cloudflare Access and the application login. |
| AC-02 | A valid new site receives a URL in the form https://{slug}.partnersflex.space and a generated password. |
| AC-03 | The service rejects invalid, reserved, and duplicate slugs. |
| AC-04 | A valid ZIP with a root index.html deploys and serves its files. |
| AC-05 | A single HTML file deploys as index.html. |
| AC-06 | A visitor must enter the correct site credentials before Nginx serves protected content. |
| AC-07 | Credentials for one site do not grant access to another site. |
| AC-08 | A failed upload does not change the active release. |
| AC-09 | The service rejects ZIP path traversal, prohibited file types, and case-only duplicate paths. |
| AC-10 | The service returns a controlled error for an unknown or disabled subdomain, and Nginx does not log a missing-file error. |
| AC-11 | A slug change updates the website URL and does not move or corrupt its releases. |
| AC-12 | A permanent delete removes access to the site and its files. It does not change other sites. |
| AC-13 | A site change (create, rename, disable, delete) works without an Nginx reload. |
| AC-14 | Other visionp.uk services continue to work. The visionp.uk DNS zone has no change. |
| AC-15 | Deferred: a backup restores the database and one site release. |
| AC-16 | A repeated request without credentials returns 401. Cloudflare does not serve a cached copy. |
| AC-17 | A disabled site is not reachable. An enabled site is reachable again with the same credentials. |
| AC-18 | A ZIP with one top-level directory and index.html deploys. A ZIP with other layouts is rejected. __MACOSX entries are ignored. |
| AC-19 | A ZIP larger than 50 MB, or one that exceeds another limit, is rejected before extraction completes. |
| AC-20 | After a deployment, only the active release and two previous releases remain. |
| AC-21 | A regenerated password replaces the old one. The old password no longer works. |
| AC-22 | The audit log records create, deploy, rename, disable, enable, delete, and credential events, and holds no passwords or tokens. |
| AC-23 | Responses from admin.partnersflex.space contain frame-ancestors 'none' and X-Frame-Options: DENY. A hosted site cannot show the admin panel in a frame. |
| AC-24 | A username with a colon, a space, a newline, or another prohibited character is rejected. The .htpasswd file stays valid. |
| AC-25 | An admin session ends after 30 minutes without use and after 12 hours in total. A login issues a new session identifier. |
| AC-26 | A direct connection to the VPS without the zone client certificate is rejected, on IPv4 and IPv6. A connection that uses the shared Cloudflare certificate is also rejected. |
| AC-27 | Nginx accepts the correct {SSHA} site password and rejects a wrong one. |
| AC-28 | With the default configuration, a request without a valid Cloudflare Access token is rejected by the application. |
| AC-29 | After a failure between the symlink switch and the database update, the service start corrects current_release_id, removes unused ready releases, and then rebuilds the by-slug links in the order of section 8.2. A disabled site stays disabled. A failed slug change leaves no old link. |

# 16. Risks and Design Decisions

| Risk | Control |
| --- | --- |
| Disk capacity | A single VPS stores all releases and staging files. Check free space before deployment. Keep at most three releases for each site. Set upload limits. |
| No backups | The MVP has no backups. A VPS failure or a mistake can destroy all data. Add backups before important use. |
| No alerts | The MVP has no alerting. A full disk or a stopped service can go unnoticed. Check the service by hand and check disk space before each deployment. |
| Archive attacks | A ZIP file can contain unsafe paths or a large expansion. Validate every entry and enforce limits using the bytes actually read. |
| Admin and site isolation | Hosted JavaScript runs in visitors’ browsers and can set domain-wide cookies. Use strict session, cookie, CSRF, and host checks. Use a separate domain for untrusted third-party content. |
| Cloudflare cache | Cloudflare can cache static files. Use a Bypass cache rule and no-store headers. Test the result. |
| Password check cost | Nginx checks the hash on every request. Use a low-cost hash and long random passwords. |
| Direct access to the VPS | A visitor could bypass Cloudflare. Use Authenticated Origin Pulls on the new server blocks. |
| Clickjacking | A hosted site could frame the admin panel. Send frame-ancestors 'none' and X-Frame-Options: DENY. Require the site slug to confirm a delete. |
| Shared VPS | Other services use the same Nginx and VPS. Add new server blocks only. Do not change existing ones. |

## 16.1 Decisions for the MVP

| Decision | MVP choice |
| --- | --- |
| Hosting model | One VPS. |
| Domain | Dedicated domain partnersflex.space. The visionp.uk zone is not changed. |
| Admin host | admin.partnersflex.space. |
| Static file server | Nginx. |
| Site routing | One regular-expression server block and by-slug symbolic links. No reload for site changes. |
| Backend | Go with net/http. |
| Admin UI | Go templates and HTMX. |
| Admin access | Cloudflare Access plus application login. |
| Metadata | SQLite. |
| Site storage | Local file system with UUID-based directories and release folders. |
| Site protection | Separate HTTP Basic Auth credentials per site. Passwords generated by the system. |
| Site deletion | Disable first. Then delete permanently with confirmation. |
| ZIP layout | Root index.html, or exactly one top-level directory. |
| Limits | 50 MB ZIP, 250 MB extracted, 5,000 files, 100:1 ratio. |
| Release retention | Active release and two previous releases. |
| Cache | Cloudflare Bypass cache plus no-store headers. |
| TLS | Cloudflare proxy, Universal SSL, and Origin Certificate. TLS 1.2 and 1.3 only. |
| Origin protection | Zone-level Authenticated Origin Pulls with a custom certificate, and real_ip with Cloudflare ranges (IPv4 and IPv6). |
| Site password hash | {SSHA} in htpasswd, written by the Go service. |
| Admin sessions | Server-side sessions in SQLite. Idle timeout 30 minutes. Absolute lifetime 12 hours. |
| Audit log | Required in the MVP. |
| Process manager | systemd. |
| Container runtime | Not used in the MVP. |

## 16.2 Deferred Items

| Item | Status |
| --- | --- |
| Backups and restore | Deferred. Add before important use. |
| Monitoring and alert delivery | Deferred. Minimal logs only. |
| Restore of a prior release | Deferred. |
| Separate admin domain | Consider if untrusted content is hosted. |

# 17. Glossary

| Term | Meaning |
| --- | --- |
| Admin panel | The web interface for site management. |
| Basic Auth | HTTP authentication that asks a visitor for a username and password. |
| Release | A complete set of files for one published version of a site. |
| Slug | The unique name that forms the first label of a site host name. |
| Staging | A temporary area for files before deployment. |
| Origin Certificate | A certificate that secures the connection from Cloudflare to the VPS. |
| Cloudflare Access | A Cloudflare service that restricts a host name to approved identities. |
| by-slug link | A symbolic link from a slug to the site directory. Nginx uses it to find the site. |
| Atomic switch | A file-system operation that changes the active release without a partial state. |
| Zip Slip | A path traversal attack that attempts to write archive files outside the target directory. |

# 18. Final Recommendation

Build the MVP as one Go application on the existing VPS. Use Nginx to serve all static site files. Use Cloudflare for the partnersflex.space DNS and edge TLS. Use SQLite for metadata and local storage for site releases.

Keep the first release small. Implement safe ZIP handling, atomic deployment, per-site access control, and strict separation between the admin panel and website content. Add backups, alerting, and release restore after the core workflow is stable. The audit log is part of the MVP.

> **Definition of done**
> An administrator can sign in, create a site, upload a ZIP or HTML file, receive the generated site credentials, and publish the site at its assigned partnersflex.space subdomain. The site works over HTTPS. Other visionp.uk services remain unchanged.
