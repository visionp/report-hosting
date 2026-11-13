# VISIONP

## Static Website Hosting and Administration Service

*Requirements and proposed technical solution*

| Document status | Proposed |
| --- | --- |
| Version | 1.0 |
| Date | 13 November 2026 |
| Product name | visionp |
| Primary domain namespace | report.visionp.uk |
| Target platform | Debian or Ubuntu VPS |
| Backend language | Go |

> **Purpose**
> This document defines the problem, the system requirements, and the proposed solution for visionp. The service will host up to 20 static websites on the existing VPS. It will use the report.visionp.uk namespace. It will use the current VPS, Nginx, Go, SQLite, and Cloudflare.

> **Writing note.** This document uses short sentences, consistent terms, and direct instructions. It follows the writing principles of ASD-STE100. It is not a certified STE document. A formal compliance review requires the current standard and an STE checker.

# 1. Executive Summary

visionp is a small website hosting service. It will run on the existing VPS. An administrator will use a web panel to create a site, upload its files, set access credentials, and manage its address.

The service will support two upload types:

- A ZIP archive that contains a static website.
- A single HTML file. The service will publish this file as index.html.

Nginx will serve website files. The Go application will manage sites and deployments. SQLite will store site metadata. The file system will store website releases. Cloudflare will provide DNS proxying and TLS at the edge.

The service will use these host names:

| Purpose | Host name |
| --- | --- |
| Admin panel | admin.report.visionp.uk |
| Hosted website | {slug}.report.visionp.uk |
| Base namespace | report.visionp.uk |
| Wildcard DNS and certificate name | *.report.visionp.uk |

> **Main design decision**
> Use one VPS and one service stack. Do not use Docker for the MVP. Do not change DNS records for other visionp.uk services. Use Nginx for static traffic and Go for administration.

# 2. Problem Statement

The project needs a simple way to publish and manage static websites. The current VPS must host these websites without a separate server for each site.

The administrator needs one place to perform these tasks:

- Create a website and assign a unique subdomain.
- Upload a ZIP archive or one HTML file.
- Set or change the website login and password.
- Publish a new version without a long service outage.
- View the site address and deployment status.
- Delete a website.

The system must keep hosted websites separate from the admin panel. It must also avoid changes to other services that use visionp.uk subdomains.

# 3. Goals and Out of Scope

## 3.1 Goals

- Host up to 20 static websites on one existing VPS.
- Use Go for the backend service.
- Support ZIP and single-HTML uploads.
- Use a unique subdomain for each website.
- Protect each website with its own HTTP Basic Authentication credentials.
- Use a simple admin panel with no separate JavaScript build system.
- Avoid new recurring infrastructure costs. Use the existing VPS and domain.
- Use safe and repeatable deployment steps.

## 3.2 Out of Scope for the MVP

- Dynamic application hosting, PHP, Node.js, or server-side website code.
- User registration or public account creation.
- Multiple administrators with role-based access control.
- Custom domains outside report.visionp.uk.
- Automated Git deployment or CI/CD integration.
- An object storage service or a separate hosting cluster.
- A visual website builder or an online HTML editor.

# 4. Scope and System Boundaries

## 4.1 Constraints

| Item | Decision |
| --- | --- |
| Operating system | Debian or Ubuntu on the existing VPS. |
| Backend | Go. |
| HTTP server | Nginx. |
| Metadata store | SQLite. |
| File storage | Local VPS file system. |
| DNS and edge TLS | Cloudflare. |
| Service manager | systemd. |
| Expected site count | Up to 20 sites. Traffic volume is not defined; confirm VPS capacity before launch. |
| Deployment model | One VPS. No Docker in the MVP. |

## 4.2 Domain Rules

Use only the report.visionp.uk namespace for this project. Do not change the root domain or other subdomains.

```text
Admin:       admin.report.visionp.uk
Website:     {slug}.report.visionp.uk
Wildcard:    *.report.visionp.uk
```

The slug must contain 1 to 32 lower-case letters, digits, or hyphens. The slug must start and end with a letter or digit. A hyphen must not appear twice in a row. Reserve the slug admin. Reserve any other platform names before launch.

Each website must use one subdomain label. Do not support nested names such as shop.demo.report.visionp.uk in the MVP.

# 5. Functional Requirements

The words MUST, SHOULD, and MAY have the following meaning in this document: MUST is required for acceptance. SHOULD is recommended. MAY is optional.

| ID | Requirement |
| --- | --- |
| FR-01 | The administrator MUST be able to sign in to the admin panel. |
| FR-02 | The administrator MUST be able to create a site with a name and a unique slug. |
| FR-03 | The system MUST reject an invalid or duplicate slug. |
| FR-04 | The administrator MUST be able to upload a ZIP archive. |
| FR-05 | The administrator MUST be able to upload one HTML file. The system MUST publish it as index.html. |
| FR-06 | The system MUST validate an archive before it publishes any file. |
| FR-07 | The system MUST require a root-level index.html in a ZIP archive. |
| FR-08 | The administrator MUST be able to set and change the site username and password. |
| FR-09 | Each site MUST have separate HTTP Basic Authentication credentials. |
| FR-10 | The system MUST publish a new release by an atomic switch to the current release. |
| FR-11 | The administrator MUST be able to view the site name, slug, URL, status, and last deployment time. |
| FR-12 | The administrator MUST be able to change a site slug. The system MUST update the route safely. |
| FR-13 | The administrator MUST be able to disable and delete a site. |
| FR-14 | The system MUST return a controlled error for an unknown site host. |
| FR-15 | The system SHOULD keep prior releases for a limited period. A restore function MAY be added after the MVP. |

# 6. Proposed Architecture

## 6.1 Main Components

| Component | Responsibility |
| --- | --- |
| Cloudflare DNS and proxy | Resolve the project host names. Proxy HTTP traffic to the VPS. Provide edge TLS. |
| Nginx | Terminate origin TLS. Route requests. Enforce site Basic Auth. Serve static files. |
| Go application | Provide the admin UI and API. Validate uploads. Manage metadata and releases. |
| SQLite | Store site data and release metadata. |
| Local file system | Store staged uploads, releases, and site password files. |
| systemd | Start, stop, and restart the Go application. |

## 6.2 Request Flow

```text
Administrator browser
        | HTTPS
        v
Cloudflare  --->  Nginx  --->  Go admin application
                         (admin.report.visionp.uk)

Website visitor
        | HTTPS
        v
Cloudflare  --->  Nginx  --->  Basic Auth  --->  Static files
                         ({slug}.report.visionp.uk)
```

Nginx must serve static files directly. Do not send normal website requests to the Go application. This reduces application load and keeps the request path simple.

## 6.3 Backend Technology

| Area | Proposed technology |
| --- | --- |
| HTTP server | Go standard library net/http. Use chi only if routing needs it. |
| Templates | Go html/template. |
| UI updates | HTMX. Avoid a separate Node.js build step. |
| Database | SQLite with modernc.org/sqlite. |
| Logging | log/slog to standard output and journald. |
| Password file | Use a tested Nginx-compatible htpasswd format. |
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
  staging/
  generated/
  empty/
```

Use a UUID for each site directory. Do not use the slug as the storage key. This allows the administrator to change a slug without moving the site files.

The current link points to the active release. Nginx reads files from this link. Keep .htpasswd outside the release directory. Do not expose it through a website URL.

## 7.2 Database Tables

| Table | Main fields | Notes |
| --- | --- | --- |
| sites | id, name, slug, auth_username, status, current_release_id, created_at, updated_at | Store no plain-text password and no password hash. |
| releases | id, site_id, version, storage_path, checksum, created_at | Use a unique version for each site. |
| audit_events (optional) | id, actor, action, site_id, created_at | Record admin actions for later review. |

Store the password hash in the site .htpasswd file. Limit read access to the service account and Nginx. Store the SQLite database in a directory that Nginx cannot serve.

# 8. Upload and Deployment Process

## 8.1 ZIP Validation

The service MUST validate the full archive before it publishes files. Use safe extraction rules:

- Reject absolute paths and paths that contain a parent-directory segment such as ../.
- Reject paths that contain a null byte or a backslash.
- Reject symbolic links and special file types. Accept regular files and directories only.
- Reject duplicate paths and attempts to replace platform-managed files.
- Require index.html at the archive root. Do not guess or remove a top-level folder.
- Limit the archive size, extracted size, file count, and compression ratio.
- Do not extract files outside the staging directory.

Suggested initial limits are 100 MB per ZIP, 250 MB of extracted data, 5,000 files, and a maximum expansion ratio of 100:1. Make these values configurable. Review them after the first use period.

## 8.2 Release Deployment

1. Receive the upload in a unique staging directory.
1. Validate the upload and extract it to a new release directory.
1. Check that the release contains a valid root-level index.html.
1. Calculate a checksum and record release metadata in SQLite.
1. Create a temporary symlink to the new release.
1. Atomically replace the current symlink with the new symlink.
1. Record the deployment result and remove temporary files.

Keep the previous release until the new release passes its checks. A failed upload or validation step MUST NOT change the active site.

## 8.3 Single HTML Upload

For a single HTML upload, create a new release directory. Save the uploaded file as index.html. Then use the normal release deployment process.

# 9. Nginx and DNS Design

## 9.1 Cloudflare DNS

Create one wildcard DNS record for the project namespace:

```text
Type:      A
Name:      *.report
Target:    <VPS_PUBLIC_IP>
Proxy:     Enabled
```

The wildcard record must not replace or modify records for other visionp.uk services. Add an explicit admin.report record only if a separate DNS target or policy is required.

## 9.2 TLS

Use a Cloudflare Origin Certificate for the connection from Cloudflare to Nginx. The certificate must cover report.visionp.uk and *.report.visionp.uk, or the exact host names used by the service. Set Cloudflare SSL/TLS mode to Full (strict). Visitors receive the Cloudflare edge certificate. Do not use the Origin Certificate for direct browser access to the VPS.

The wildcard name *.report.visionp.uk covers admin.report.visionp.uk and demo.report.visionp.uk. It does not cover report.visionp.uk or a nested name such as shop.demo.report.visionp.uk.

## 9.3 Nginx Virtual Hosts

Use separate Nginx server blocks:

- An exact server_name for admin.report.visionp.uk. Proxy requests to the Go application on a local address.
- A wildcard server_name for hosted websites. Serve files from the site storage path. Enforce Basic Auth.
- A default server that rejects unknown host names.

Use separate host-to-site maps for the document root and the .htpasswd file. Resolve each valid site host to its UUID storage path. Use an empty directory and a deny credentials file as the defaults for unknown hosts. Do not build file paths directly from untrusted request data.

When a site is created, deleted, or renamed, update the generated site map. Validate the Nginx configuration before reload. Reload Nginx only after validation succeeds.

> **Privilege boundary**
> The Go process must not write to arbitrary files in /etc/nginx and must not run unrestricted commands as root. Use a narrow, root-owned helper. The helper must validate a strict site manifest, write the Nginx include, run nginx -t, and reload Nginx only when the test passes.

## 9.4 Cache Policy

Disable Cloudflare caching for protected sites in the MVP. Do not cache a response before the Basic Auth check. Add a tested cache policy later if public, cacheable sites become a requirement.

# 10. Security Requirements

| Area | Requirement |
| --- | --- |
| Admin access | Require an admin login. Do not allow public registration. Cloudflare Access or an IP allowlist MAY add another access layer. |
| Session cookies | Use Secure, HttpOnly, SameSite cookies. Use the __Host- prefix, Path=/, and no Domain attribute. Do not set a parent-domain cookie. |
| Request validation | Validate Host and Origin. Add CSRF protection to state-changing requests. Do not enable wildcard CORS. |
| Site access | Use separate Basic Auth credentials for each site. Do not store plain-text passwords. |
| Password hashes | Use a hash format that the installed Nginx build supports. Verify authentication with an integration test. |
| Upload isolation | Use a staging directory. Reject archive traversal, links, special files, and unsafe paths. |
| File access | Run Go as a dedicated service user. Give Nginx read-only access to released files. Keep secrets and the database outside the web root. |
| Dotfiles | Deny requests for hidden files. Do not serve .htpasswd, database files, logs, or generated configuration. |
| Rate limits | Apply login and upload rate limits. Use Nginx limits where they are effective. |
| Logs | Do not log passwords, session tokens, or full Authorization headers. |
| Site isolation | Use one subdomain per site. Keep admin cookies host-only with the __Host- prefix. Treat uploaded HTML and JavaScript as untrusted browser content. |

Important isolation note: Sites and the admin panel share the report.visionp.uk namespace. A site can run arbitrary browser code. The __Host- cookie prefix and strict CSRF checks reduce cookie and request risks. They do not provide the same isolation as a separate registrable domain. Use a separate domain if the service later hosts content from untrusted users.

# 11. Admin UI and API

## 11.1 Admin UI

Use server-rendered HTML templates. Use HTMX for small updates. The MVP UI should provide:

- A site list with name, slug, URL, status, and update time.
- A create-site form.
- An upload form for ZIP or HTML files.
- A credentials form to set or change the site login.
- A site detail page with the public URL and current release.
- A delete action with a confirmation step.

## 11.2 API Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | /api/sites | List sites. |
| POST | /api/sites | Create a site. |
| GET | /api/sites/{id} | Get site details. |
| PATCH | /api/sites/{id} | Change site name or slug. |
| DELETE | /api/sites/{id} | Disable and delete a site. |
| POST | /api/sites/{id}/deploy | Upload and deploy a new release. |
| POST | /api/sites/{id}/credentials | Set or change site credentials. |
| GET | /api/sites/{id}/releases | List releases. Optional for the MVP. |
| POST | /api/sites/{id}/restore | Restore a prior release. Later phase. |

All admin API routes must require an authenticated session. State-changing routes must use CSRF protection. Return clear validation errors. Do not expose server paths or internal error details to the browser.

# 12. Deployment and Operations

## 12.1 Service Accounts and Permissions

Run the Go application as a dedicated visionp user. Do not run it as root. Use a dedicated group to grant Nginx read access to released website files. Give write access only to the paths that the Go service needs.

## 12.2 systemd

Run the application as a systemd service. Use a fixed working directory and an explicit configuration file. Enable automatic restart after a process failure. Send logs to journald. Keep secrets in a root-readable environment file or another protected secret store.

## 12.3 Backups

- Back up the SQLite database and site releases.
- Use a consistent backup method for SQLite. Use SQLite backup support or stop the service during a file copy.
- Keep backups outside the active site directory.
- Copy at least one backup set to a different system. A local copy alone does not protect against VPS loss.
- Test a restore before the service is used for important sites.

## 12.4 Monitoring

- Check the Go service state and Nginx state.
- Log upload, validation, deployment, rename, and delete events.
- Monitor free disk space. Alert before the VPS disk becomes full.
- Monitor failed logins, failed uploads, and Nginx reload errors.

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
    nginx/
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

Keep business rules in service packages. Keep HTTP handlers thin. Keep Nginx configuration generation separate from site and upload logic. Add tests for archive validation and deployment before the admin UI is complete.

# 14. Implementation Plan

| Phase | Deliverables |
| --- | --- |
| Phase 1 - VPS and network | Create the project DNS record. Install the origin certificate. Add Nginx virtual hosts. Create storage paths and the systemd unit. |
| Phase 2 - Backend core | Create the Go application. Add SQLite migrations. Implement site CRUD, configuration loading, and filesystem storage. |
| Phase 3 - Upload and deployment | Add ZIP validation and extraction. Add single-HTML upload. Implement release creation and atomic activation. |
| Phase 4 - Nginx integration | Generate the site map. Add per-site Basic Auth. Add safe Nginx test and reload handling. |
| Phase 5 - Admin UI | Add login, site list, site creation, upload, credentials, status, and delete actions. |
| Phase 6 - Hardening | Add rate limits, backups, audit logs, disk checks, and security tests. |

# 15. Acceptance Criteria

| ID | Acceptance test |
| --- | --- |
| AC-01 | The admin panel opens at https://admin.report.visionp.uk. |
| AC-02 | A valid new site receives a URL in the form https://{slug}.report.visionp.uk. |
| AC-03 | The service rejects invalid and duplicate slugs. |
| AC-04 | A valid ZIP with a root index.html deploys and serves its files. |
| AC-05 | A single HTML file deploys as index.html. |
| AC-06 | A visitor must enter the correct site credentials before Nginx serves protected content. |
| AC-07 | Credentials for one site do not grant access to another site. |
| AC-08 | A failed upload does not change the active release. |
| AC-09 | The service rejects ZIP path traversal and prohibited file types. |
| AC-10 | The service returns a controlled error for an unknown subdomain. |
| AC-11 | A slug change updates the website URL and does not move or corrupt its releases. |
| AC-12 | A site delete removes access to the site. It does not change other sites. |
| AC-13 | Nginx reload occurs only after nginx -t succeeds. |
| AC-14 | Other visionp.uk services continue to work after setup and deployment. |
| AC-15 | A backup can restore the database and at least one site release. |

# 16. Risks and Design Decisions

| Risk | Control |
| --- | --- |
| Disk capacity | A single VPS stores all releases and staging files. Check free space before deployment. Set upload and retention limits. Monitor disk use. |
| Archive attacks | A ZIP file can contain unsafe paths or a large expansion. Validate every entry and enforce limits before extraction. |
| Nginx reload | A bad generated configuration can affect all hosted sites. Test configuration before every reload. Keep the last valid configuration. |
| Admin and site isolation | Hosted JavaScript runs in visitors’ browsers. Use strict session, cookie, CSRF, and host checks. Use a separate registrable domain for untrusted third-party content. |
| VPS failure | The service has one compute host. Keep off-host backups and document a restore process. |
| Cloudflare settings | A wrong DNS or cache rule can affect other services or expose protected content. Limit all changes to the report namespace and test cache bypass. |

## 16.1 Decisions for the MVP

| Decision | MVP choice |
| --- | --- |
| Hosting model | One VPS. |
| Static file server | Nginx. |
| Backend | Go with net/http. |
| Admin UI | Go templates and HTMX. |
| Metadata | SQLite. |
| Site storage | Local file system with UUID-based directories and release folders. |
| Site protection | Separate HTTP Basic Auth credentials per site. |
| TLS | Cloudflare proxy and Origin Certificate. |
| Process manager | systemd. |
| Container runtime | Not used in the MVP. |

# 17. Glossary

| Term | Meaning |
| --- | --- |
| Admin panel | The web interface for site management. |
| Basic Auth | HTTP authentication that asks a visitor for a username and password. |
| Release | A complete set of files for one published version of a site. |
| Slug | The unique name that forms the first label of a site host name. |
| Staging | A temporary area for files before deployment. |
| Origin Certificate | A certificate that secures the connection from Cloudflare to the VPS. |
| Atomic switch | A file-system operation that changes the active release without a partial state. |
| Zip Slip | A path traversal attack that attempts to write archive files outside the target directory. |

# 18. Final Recommendation

Build the MVP as one Go application on the existing VPS. Use Nginx to serve all static site files. Use Cloudflare for the project DNS namespace and edge TLS. Use SQLite for metadata and local storage for site releases.

Keep the first release small. Implement safe ZIP handling, atomic deployment, per-site access control, and strict separation between the admin panel and website content. Add release restore, audit history, and advanced cache rules after the core workflow is stable.

> **Definition of done**
> An administrator can sign in, create a site, upload a ZIP or HTML file, set site credentials, and publish the site at its assigned report.visionp.uk subdomain. The site works over HTTPS. Other visionp.uk services remain unchanged.
