# report_hosting MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that lets one administrator create password-protected static sites on `{slug}.partnersflex.space`, deploy ZIP or single-HTML releases atomically, and manage them from an admin panel at `admin.partnersflex.space`.

**Architecture:** One Go binary (`net/http`, `html/template`, HTMX, SQLite) manages sites and releases on the local file system. Nginx serves all site traffic directly through one regex `server_name`, `by-slug/{slug}` symlinks, and per-site `.htpasswd` files. Go never reloads Nginx and never runs as root.

**Tech Stack:** Go 1.22+ (plan decision: `ServeMux` method patterns), `modernc.org/sqlite`, `golang.org/x/crypto/argon2`, `github.com/golang-jwt/jwt/v5` (Access token check), HTMX file vendored in `web/static`, Nginx 1.18+, systemd, Cloudflare.

**Spec:** `report_hosting_technical_specification.md` (version 1.2). Section numbers below (§) refer to it.

**Module path:** `report_hosting` (set by the user). Imports look like `report_hosting/internal/domain`. The binary and the service keep the name `report_hosting` (`cmd/report_hosting`, `report_hosting.service`).

## Global Constraints

Every task includes these. Values are copied from the spec.

- Domain `partnersflex.space`. Admin host `admin.partnersflex.space`. Site host `{slug}.partnersflex.space`. Do not touch the `visionp.uk` zone, certificates, or existing Nginx server blocks.
- Slug: 1–32 chars of `a-z0-9-`, starts and ends with letter or digit, no `--`. Reserved: `admin, www, api, mail, ftp, status, cdn, static, ns1, ns2` (configurable). Username: `a-z0-9-`, 1–32 chars.
- ZIP limits (configurable): 50 MB archive, 250 MB extracted, 5,000 files, 100:1 ratio. Count bytes actually read. Reject path traversal, absolute paths, NUL, backslash, symlinks, special files, duplicates (case-insensitive), `index.html` missing. Ignore `__MACOSX` and `.DS_Store`. Accept root `index.html` or exactly one top-level directory containing it.
- Release retention: active release plus two previous. Checksum: SHA-256. Staging and sites on the same file system.
- Site password: `crypto/rand`, 20+ chars, alphabet ≥64 symbols (`A-Za-z0-9-_`, no colon). Hash format `{SSHA}`. Replace `.htpasswd` by temp file plus `rename()`.
- Admin: argon2id (64 MiB, 3 iterations, parallelism 2). Lockout: 5 failures in 15 min locks 15 min, per address and per account. Sessions: server-side in SQLite, idle 30 min, absolute 12 h, new ID on login, cookie `__Host-` with `Secure; HttpOnly; SameSite=Strict; Path=/`, no `Domain`.
- Cloudflare Access JWT check (signature, `iss`, `aud`, `exp`) is on by default and mandatory in production mode.
- File modes: directories 0750, files 0640 (umask 027). `db/` and `staging/` 0700. Nginx 1.18+, TLS 1.2 and 1.3 only.
- Admin responses carry `Content-Security-Policy: frame-ancestors 'none'` and `X-Frame-Options: DENY`. Site responses carry `Cache-Control: private, no-store`.
- Never log passwords, session tokens, `Authorization`, `Cf-Access-Jwt-Assertion`, `Cf-Access-Authenticated-User-Email`.
- No Docker in the deployment. Backups and alerting are out of scope (§12.3, §12.4).

## Review Focus

Input classes the spec implies but does not test. Each has a test in the owning task.

1. ZIP whose header lies about sizes (zip bomb) → rejected by counting real bytes (Task 4).
2. ZIP entries that differ only by letter case (`Index.html` and `index.html`) → rejected as duplicates (Task 4).
3. Two deployments to the same site at once → serialized, no mixed release (Task 5).
4. Disk almost full → upload rejected before extraction, active site unchanged (Task 5).
5. Single HTML upload that is empty or over the limit, or a ZIP where `index.html` is a directory → clear rejection (Tasks 4 and 5).

## File Structure

```text
go.mod (module report_hosting), Makefile
cmd/report_hosting/main.go                 # serve | admin create
migrations/001_init.sql
internal/config/config.go           # Config, Load
internal/domain/{types,validate}.go # Site, Release, errors, slug/username rules
internal/repository/store.go        # SQLite store, embedded migrations
internal/storage/layout.go          # paths
internal/storage/credentials.go     # password generation, .htpasswd
internal/storage/zipx.go            # ZIP validation and extraction
internal/storage/deploy.go          # staging -> release -> current
internal/routing/links.go           # by-slug symlinks
internal/service/{sites,recovery,auth}.go
internal/handler/{middleware,api,ui,router}.go
web/templates/*.html, web/static/htmx.min.js
deploy/nginx/report_hosting.conf, deploy/systemd/report_hosting.service
deploy/scripts/nginx-itest.sh, deploy/RUNBOOK.md
```

---

### Task 1: Scaffold, config, domain rules

**Files:**
- Create: `go.mod`, `Makefile`, `cmd/report_hosting/main.go`, `internal/config/config.go`, `internal/domain/types.go`, `internal/domain/validate.go`
- Test: `internal/domain/validate_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `domain.ValidateSlug(s string, reserved []string) error` → `ErrInvalidSlug` or `ErrReservedSlug`
  - `domain.ValidateUsername(s string) error` → `ErrInvalidUsername`
  - `domain.Site{ID, Name, Slug, AuthUsername string; Status SiteStatus; CurrentReleaseID string; CreatedAt, UpdatedAt time.Time}`, `SiteStatus` = `"active" | "disabled"`
  - `domain.Release{ID, SiteID string; Version int; StoragePath, Checksum string; Status ReleaseStatus; CreatedAt time.Time}`, `ReleaseStatus` = `"ready" | "active" | "superseded"`
  - errors: `ErrDuplicateSlug`, `ErrSlugConfirmation`, `ErrSiteNotDisabled`, `ErrNotFound`
  - `config.Load(path string) (Config, error)` with fields `DataDir, ListenAddr, BaseDomain string; Limits; ReservedSlugs []string; Production, AccessCheck bool; AccessTeamDomain, AccessAUD string; Argon2 Argon2Params`

- [ ] **Step 1: Write failing tests**
  ```go
  // validate_test.go (table tests)
  TestValidateSlug: "demo" ok; "a" ok; 32 chars ok; 33 chars ErrInvalidSlug; "-a","a-","a--b","A","a_b","" ErrInvalidSlug; "admin" ErrReservedSlug
  TestValidateUsername: "site-1" ok; "a:b","a b","a\nb","","é" ErrInvalidUsername   // AC-24
  // config_test.go
  TestLoadDefaults: limits = 50MB/250MB/5000/100; reserved list from §4.2
  TestProductionRequiresAccessCheck: Production=true, AccessCheck=false -> Load returns error
  ```
- [ ] **Step 2: Run** `go test ./internal/... -v` → FAIL (undefined)
- [ ] **Step 3: Implement** the signatures above. Config is a TOML or JSON file plus env override for secrets (decide: JSON file, `REPORT_HOSTING_*` env). `AccessCheck` defaults to true.
- [ ] **Step 4: Run** `go test ./internal/... -v` → PASS
- [ ] **Step 5: Commit** `feat: scaffold, config, slug and username rules`

### Task 2: SQLite store and migrations

**Files:**
- Create: `migrations/001_init.sql`, `internal/repository/store.go`
- Test: `internal/repository/store_test.go`

**Interfaces:**
- Consumes: `domain` types.
- Produces (`*repository.Store`, all take `ctx context.Context` first):
  - `Open(path string) (*Store, error)` (WAL, foreign keys on, embedded migrations)
  - Sites: `CreateSite(Site) error` (`ErrDuplicateSlug`), `GetSite(id)`, `GetSiteBySlug(slug)`, `ListSites()`, `UpdateSite(id, name, slug string)`, `SetSiteStatus(id string, SiteStatus)`, `SetAuthUsername(id, username string)`, `DeleteSite(id)`
  - Releases: `AddRelease(Release) error`, `ListReleases(siteID) []Release`, `ActivateRelease(siteID, releaseID string) error` (one transaction: previous active → `superseded`, target → `active`, `sites.current_release_id` set), `DeleteRelease(id)`, `ListReleasesByStatus(status) []Release`, `NextVersion(siteID) (int, error)`
  - Admin: `CreateAdmin(username, hash string)`, `GetAdmin(username)`
  - Sessions: `PutSession(idHash string, created, lastSeen, expires time.Time)`, `GetSession(idHash)`, `TouchSession(idHash, time.Time)`, `DeleteSession(idHash)`
  - Attempts: `RecordFailure(key string, now time.Time, window time.Duration) (count int, err error)`, `Lock(key string, until time.Time)`, `LockedUntil(key string) (time.Time, error)`, `ClearFailures(key)`
  - Audit: `AddAudit(actor, action, siteID string)`

- [ ] **Step 1: Write failing tests**
  ```go
  TestCreateSiteDuplicateSlug: second CreateSite same slug -> ErrDuplicateSlug
  TestActivateReleaseMarksPreviousSuperseded: activate v1 then v2 -> v1 superseded, v2 active, site.CurrentReleaseID == v2
  TestNextVersionPerSite: two sites each start at 1
  TestDeleteSiteCascadesReleases
  TestSessionExpiryColumnsRoundTrip
  ```
- [ ] **Step 2: Run** `go test ./internal/repository/ -v` → FAIL
- [ ] **Step 3: Implement** `001_init.sql` with tables from §7.2 (`sites`, `releases`, `admins`, `admin_sessions`, `login_attempts`, `audit_events`). Unique index on `sites.slug` and on `releases(site_id, version)`.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: sqlite store and migrations`

### Task 3: Password generation and `.htpasswd`

**Files:**
- Create: `internal/storage/layout.go`, `internal/storage/credentials.go`
- Test: `internal/storage/credentials_test.go`

**Interfaces:**
- Produces:
  - `storage.Layout{Root string}` with `SiteDir(id)`, `ReleaseDir(siteID string, version int)`, `StagingDir()`, `BySlugDir()`, `HtpasswdPath(siteID)`
  - `storage.NewPassword() (string, error)` — 20 chars from the 64-symbol alphabet
  - `storage.WriteHtpasswd(siteDir, username, password string) error` — writes `username:{SSHA}...` to a temp file in the same directory, `chmod 0640`, `rename()`
  - `storage.VerifySSHA(line, password string) bool`

- [ ] **Step 1: Write failing tests**
  ```go
  TestNewPasswordLengthAndAlphabet: len==20, every char in A-Za-z0-9-_, no ':'; 1000 calls produce 1000 distinct values
  TestWriteHtpasswdFormat: file is exactly one line "user:{SSHA}<base64>"; VerifySSHA true for the password, false for another
  TestWriteHtpasswdAtomic: while a goroutine reads in a loop during 200 rewrites, every read is a complete valid line (never empty/partial)
  TestWriteHtpasswdRejectsBadUsername: "a:b" -> ErrInvalidUsername, file unchanged   // AC-24
  TestWriteHtpasswdMode: file mode 0640
  ```
- [ ] **Step 2: Run** `go test ./internal/storage/ -run 'Password|Htpasswd' -v` → FAIL
- [ ] **Step 3: Implement.** SSHA = base64(sha1(password+salt)+salt), 8-byte `crypto/rand` salt.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: password generation and htpasswd writer`

### Task 4: ZIP validation and extraction

**Files:**
- Create: `internal/storage/zipx.go`
- Test: `internal/storage/zipx_test.go` (build ZIPs in memory with `archive/zip`; hand-craft headers for the lying-size case)

**Interfaces:**
- Consumes: `config.Limits{MaxZipBytes, MaxExtractedBytes int64; MaxFiles, MaxRatio int}`
- Produces:
  - `storage.ExtractZip(zipPath, stagingDir string, lim Limits) (siteRoot string, err error)` — extracts inside `stagingDir`, returns the directory that holds `index.html` (the staging dir or its single top-level directory)
  - `storage.StageHTML(r io.Reader, stagingDir string, maxBytes int64) (siteRoot string, err error)` — writes `index.html`
  - sentinel errors: `ErrUnsafePath, ErrSpecialFile, ErrDuplicatePath, ErrLimitExceeded, ErrNoIndex, ErrBadLayout, ErrEmptyUpload`

- [ ] **Step 1: Write failing tests**
  ```go
  TestRejectsTraversal: "../x", "/abs", "a\\b", "a\x00b" -> ErrUnsafePath            // AC-09
  TestRejectsSymlinkAndSpecial: symlink entry, device mode -> ErrSpecialFile
  TestRejectsCaseOnlyDuplicates: "Index.html"+"index.html" -> ErrDuplicatePath        // Review Focus 2
  TestZipBombHeaderLies: header says 1 KB, stream is 300 MB -> ErrLimitExceeded, nothing left outside stagingDir   // Review Focus 1
  TestLimits: 5001 files, >250 MB, ratio >100:1, file >50 MB archive -> ErrLimitExceeded   // AC-19
  TestRootIndex: root index.html ok; siteRoot == stagingDir                            // AC-04
  TestSingleTopDir: "site/index.html" ok; siteRoot == stagingDir/site                  // AC-18
  TestRejectsOtherLayouts: two top dirs; "a/b/index.html"; no index -> ErrBadLayout/ErrNoIndex
  TestIgnoresJunk: "__MACOSX/x", ".DS_Store" ignored and not extracted; "__MACOSX" + "site/" still counts as one top dir
  TestIndexIsDirectory: entry "index.html/" -> ErrNoIndex                              // Review Focus 5
  TestStageHTML: content written as index.html; empty -> ErrEmptyUpload; over maxBytes -> ErrLimitExceeded   // AC-05
  TestExtractedFileModes: files 0640, dirs 0750
  ```
- [ ] **Step 2: Run** `go test ./internal/storage/ -run 'Zip|HTML|Rejects|Limits|Root|Single|Ignores|Index' -v` → FAIL
- [ ] **Step 3: Implement** with a streaming copy through a counting reader (abort at limit), canonical-path check against `stagingDir`, and a lower-cased path set for duplicates. Validate every entry before writing the first file.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: safe zip validation and extraction`

### Task 5: Release deployment pipeline

**Files:**
- Create: `internal/storage/deploy.go`
- Test: `internal/storage/deploy_test.go`

**Interfaces:**
- Consumes: `Layout`, `ExtractZip`, `StageHTML`, `repository.Store`.
- Produces:
  - `storage.Deployer{Layout; Store *repository.Store; Limits; MinFreeBytes int64}` and `NewDeployer(...) *Deployer`
  - `(d *Deployer) DeployZip(ctx, siteID, zipPath string) (domain.Release, error)`
  - `(d *Deployer) DeployHTML(ctx, siteID string, r io.Reader) (domain.Release, error)`
  - internal order (§8.2): staging dir → validate/extract → checksum (SHA-256 of the file tree) → `rename()` into `releases/NNNNNN` → `AddRelease(ready)` → temp symlink + `rename()` over `current` → `ActivateRelease` → cleanup → prune to active + 2 `superseded`
  - `storage.ErrInsufficientDisk`

- [ ] **Step 1: Write failing tests**
  ```go
  TestDeployZipAndHTML: after deploy, current -> releases/000001, index.html readable, release row active   // AC-04/05
  TestFailedValidationLeavesActiveRelease: deploy v1, deploy bad ZIP -> current still v1, no ready row left, staging dir removed   // AC-08
  TestSecondDeploySupersedesFirst: v1 superseded, v2 active
  TestRetention: after 5 deploys only v3(superseded), v4(superseded), v5(active) remain on disk and in DB   // AC-20
  TestConcurrentDeploysSerialized: two goroutines deploy to the same site -> versions 1 and 2, current points to a complete release, no error   // Review Focus 3
  TestDiskFullRejectedBeforeExtract: MinFreeBytes larger than free space -> ErrInsufficientDisk, active site unchanged   // Review Focus 4
  TestCrashBetweenSwitchAndDB: simulate failure after symlink switch (hook) -> row stays 'ready', current points to it (recovery handles it in Task 6)
  TestReleaseFileModes: dirs 0750, files 0640
  ```
- [ ] **Step 2: Run** `go test ./internal/storage/ -run Deploy -v` → FAIL
- [ ] **Step 3: Implement.** Per-site `sync.Mutex` map for serialization; free space via `syscall.Statfs`; failure-injection hook field `afterSwitch func() error` used only by tests.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: atomic release deployment with retention`

### Task 6: by-slug links and start recovery

**Files:**
- Create: `internal/routing/links.go`, `internal/service/recovery.go`
- Test: `internal/routing/links_test.go`, `internal/service/recovery_test.go`

**Interfaces:**
- Consumes: `Layout`, `Store`.
- Produces:
  - `routing.Links{Dir string}`; `Create(slug, siteID string) error` (relative target `../sites/{id}`, atomic via temp symlink + `rename()`), `Remove(slug string) error`, `List() (map[string]string, error)` (slug → site ID), `Rename(oldSlug, newSlug, siteID string, commit func() error) error` (create new → `commit()` → remove old; if `commit` fails, remove new and return the error)
  - `service.Recover(ctx, st *repository.Store, layout storage.Layout, links routing.Links) error` — runs the four steps of §8.2 in order

- [ ] **Step 1: Write failing tests**
  ```go
  TestRenameOrder: during Rename both slugs resolve (observed from commit callback); afterwards only the new one   // AC-11, §8.4
  TestRenameCommitFailureLeavesOldOnly
  TestRecoverAlignsCurrentToSymlink: DB says v1 active, symlink -> v2 -> after Recover DB says v2 active, v1 superseded   // AC-29
  TestRecoverRemovesUnusedReady: ready release not pointed at by current -> directory and row removed; superseded releases untouched
  TestRecoverCreatesMissingLinks: active site without link -> link created
  TestRecoverRemovesStaleLinks: link without DB site, and link of a disabled site -> removed   // AC-29, AC-17
  TestRecoverOrder: disabled site whose DB row was updated but link remains is NOT reopened
  ```
- [ ] **Step 2: Run** `go test ./internal/routing/ ./internal/service/ -run 'Links|Rename|Recover' -v` → FAIL
- [ ] **Step 3: Implement** both. `Recover` is idempotent and is called by `main` before the HTTP listener starts.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: by-slug links and start recovery`

### Task 7: Site service

**Files:**
- Create: `internal/service/sites.go`
- Test: `internal/service/sites_test.go`

**Interfaces:**
- Consumes: `Store`, `Layout`, `Links`, `Deployer`, `NewPassword`, `WriteHtpasswd`, domain validators.
- Produces `service.Sites` with:
  - `Create(ctx, actor, name, slug string) (domain.Site, password string, err error)` — site dir, generated credentials (username defaults to the slug), `.htpasswd`, by-slug link
  - `Rename(ctx, actor, id, name, slug string) error` (uses `Links.Rename`, commit = `Store.UpdateSite`)
  - `Disable(ctx, actor, id string) error` (remove link, status disabled), `Enable(ctx, actor, id string) error` (create link, status active)
  - `Delete(ctx, actor, id, confirmSlug string) error` — requires disabled (`ErrSiteNotDisabled`) and `confirmSlug == site.Slug` (`ErrSlugConfirmation`); order: remove link, delete DB row, delete files
  - `RegenerateCredentials(ctx, actor, id, username string) (password string, err error)`
  - `DeployZip/DeployHTML` pass through to `Deployer`
  - every method writes an `audit_events` row (no secrets)

- [ ] **Step 1: Write failing tests**
  ```go
  TestCreateRejectsInvalidReservedDuplicate: AC-03
  TestCreateWritesCredentialsAndLink: .htpasswd verifies returned password; link resolves; password returned once and not stored in DB   // AC-02
  TestRenameKeepsReleases: files not moved, link updated   // AC-11
  TestDisableEnableRoundTrip: link removed then restored, same credentials   // AC-17
  TestDeleteRequiresDisabledAndExactSlug: wrong slug -> ErrSlugConfirmation; enabled site -> ErrSiteNotDisabled; nothing removed
  TestDeleteOrderAndIsolation: other sites untouched; link, row, files gone   // AC-12
  TestRegenerateInvalidatesOldPassword: old password no longer verifies   // AC-21
  TestAuditEventsRecorded: create/deploy/rename/disable/enable/delete/credentials each add a row; no row text contains the password   // AC-22
  ```
- [ ] **Step 2: Run** `go test ./internal/service/ -run Sites -v` → FAIL
- [ ] **Step 3: Implement.** Use the real temp-dir file system in tests, not mocks.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: site service (create, rename, disable, delete, credentials)`

### Task 8: Admin authentication and sessions

**Files:**
- Create: `internal/service/auth.go`; modify `cmd/report_hosting/main.go` (add `admin create` subcommand)
- Test: `internal/service/auth_test.go`

**Interfaces:**
- Consumes: `Store` admin, session, attempts methods; `config.Argon2Params`.
- Produces:
  - `service.HashPassword(pw string, p Argon2Params) (string, error)`, `service.VerifyPassword(hash, pw string) bool`
  - `service.Auth{Store; Now func() time.Time; Params}` with `Login(ctx, username, password, ip string) (sessionID string, err error)` (`ErrBadCredentials`, `ErrLocked`), `Validate(ctx, sessionID string) (Session, error)` (`ErrSessionExpired`), `Logout(ctx, sessionID string) error`
  - session ID: 32 random bytes, base64url; only its SHA-256 hash is stored

- [ ] **Step 1: Write failing tests** (inject `Now`)
  ```go
  TestHashUsesArgon2idParams: hash string encodes m=65536,t=3,p=2
  TestLoginRotatesSessionID: two logins -> different IDs; old ID still independent
  TestIdleTimeout30Min: Validate at +29m ok (and extends last_seen), after 31m idle -> ErrSessionExpired   // AC-25
  TestAbsoluteLifetime12h: active use for 12h01m -> ErrSessionExpired
  TestLockoutAfterFiveFailures: 5 wrong passwords in 15 min -> 6th returns ErrLocked even with the right password; unlocked after 15 min
  TestLockoutPerAddressAndPerAccount: failures from different IPs on one account lock the account; one IP on many accounts locks the IP
  TestStoredSessionIsHashed: DB value != session ID
  ```
- [ ] **Step 2: Run** `go test ./internal/service/ -run 'Auth|Login|Session|Lockout' -v` → FAIL
- [ ] **Step 3: Implement** `report_hosting admin create <username>` — reads the password from stdin (no echo, no argument), stores the argon2id hash; refuses if an admin already exists.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: admin auth, sessions, lockout, admin create command`

### Task 9: HTTP middleware

**Files:**
- Create: `internal/handler/middleware.go`
- Test: `internal/handler/middleware_test.go`

**Interfaces:**
- Consumes: `service.Auth`, config.
- Produces:
  - `handler.AccessVerifier interface{ Verify(ctx context.Context, token string) error }` and `NewAccessVerifier(teamDomain, aud string, keys KeySource) AccessVerifier` (checks signature, `iss`, `aud`, `exp`)
  - `handler.RequireAccess(v AccessVerifier) func(http.Handler) http.Handler` (header `Cf-Access-Jwt-Assertion`)
  - `handler.RequireSession(a *service.Auth) func(http.Handler) http.Handler`
  - `handler.CSRF(...)` (per-session token in form field or `X-CSRF-Token`, plus `Origin`/`Host` check for state-changing methods)
  - `handler.SecurityHeaders` (CSP `frame-ancestors 'none'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`)
  - `handler.SessionCookie(id string) *http.Cookie` (`__Host-report_hosting_session`, `Secure; HttpOnly; SameSite=Strict; Path=/`, no Domain)
  - `handler.RedactingLogger` helper that drops the four forbidden headers

- [ ] **Step 1: Write failing tests**
  ```go
  TestAccessMissingOrInvalidTokenRejected: no header, bad signature, wrong iss, wrong aud, expired -> 403   // AC-28
  TestAccessValidTokenPasses
  TestAccessDefaultOn: zero-value Config enables the check
  TestCSRFRejectsMissingTokenAndForeignOrigin: POST without token -> 403; Origin https://x.partnersflex.space -> 403   // sites are same-site
  TestSecurityHeaders: frame-ancestors 'none' and X-Frame-Options DENY on every response   // AC-23
  TestCookieAttributes: name prefix __Host-, Secure, HttpOnly, SameSite=Strict, Path=/, Domain empty
  TestLogsNeverContainSecrets: request with Authorization, Cf-Access-Jwt-Assertion, Cf-Access-Authenticated-User-Email, password form field -> none appear in captured log output
  ```
- [ ] **Step 2: Run** `go test ./internal/handler/ -v` → FAIL
- [ ] **Step 3: Implement** with `httptest`; the key source is an interface so tests supply a local key.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: access, session, csrf and security-header middleware`

### Task 10: JSON API

**Files:**
- Create: `internal/handler/api.go`, `internal/handler/router.go`
- Test: `internal/handler/api_test.go`

**Interfaces:**
- Consumes: `service.Sites`, middleware.
- Produces: routes from §11.2 on a `ServeMux` (method patterns): `GET/POST /api/sites`, `GET/PATCH/DELETE /api/sites/{id}`, `POST /api/sites/{id}/{disable|enable|deploy|credentials}`, `GET /api/sites/{id}/releases`; `POST /login`, `POST /logout`. `handler.NewRouter(Deps) http.Handler`. Errors are JSON `{"error": "<code>"}` with no paths or internals. The deploy route caps the body with `http.MaxBytesReader` at `MaxZipBytes` and accepts `.zip` or `.html` by multipart field.

- [ ] **Step 1: Write failing tests**
  ```go
  TestAllRoutesRequireSession: every /api route without a session -> 401
  TestStateChangingRequiresCSRF
  TestCreateSiteReturnsPasswordOnce: POST /api/sites response includes password; GET /api/sites/{id} does not
  TestDeleteRequiresSlugInBody: DELETE with empty or wrong slug -> 400, site intact; exact slug on a disabled site -> 204   // FR-13
  TestDeployZipAndHTMLUpload: multipart ZIP -> 200 release; .html -> 200; oversized body -> 413
  TestDeployEmptyHTMLRejected: 0-byte HTML -> 400   // Review Focus 5
  TestErrorsHideInternals: forced storage error response contains no path or stack text
  TestUnknownSiteIs404
  ```
- [ ] **Step 2: Run** `go test ./internal/handler/ -run API -v` → FAIL
- [ ] **Step 3: Implement.** Handlers stay thin and call `service.Sites`.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `feat: admin JSON API`

### Task 11: Admin UI

**Files:**
- Create: `internal/handler/ui.go`, `web/templates/{layout,login,sites,site_detail,site_new}.html`, `web/static/htmx.min.js` (vendored, pinned version noted in `web/static/README.md`)
- Test: `internal/handler/ui_test.go`

**Interfaces:**
- Consumes: `service.Sites`, router. Templates embedded with `go:embed`.
- Produces: pages for login, site list (name, slug, URL, status, update time), create form, upload form, credentials form, site detail (URL, current release), disable/enable buttons, delete form that requires typing the slug. The generated password is shown once on the create and regenerate result pages.

- [ ] **Step 1: Write failing tests**
  ```go
  TestSiteListShowsFields: name, slug, https://{slug}.partnersflex.space, status, update time   // FR-11
  TestNoExternalAssets: no rendered page references a host other than self (HTMX from /static)
  TestDeleteFormRequiresSlugField
  TestPasswordShownOnlyOnCreateResult: second view of the same site does not contain it
  TestTemplatesEscapeHTML: site name "<script>" renders escaped
  ```
- [ ] **Step 2: Run** `go test ./internal/handler/ -run UI -v` → FAIL
- [ ] **Step 3: Implement** server-rendered templates; HTMX only for partial updates (list refresh, deploy status).
- [ ] **Step 4: Run** → PASS. Then `go run ./cmd/report_hosting serve` locally with `AccessCheck=false` and click through create → upload → disable → delete.
- [ ] **Step 5: Commit** `feat: server-rendered admin UI`

### Task 12: Nginx and systemd files, with an integration test

**Files:**
- Create: `deploy/nginx/report_hosting.conf`, `deploy/systemd/report_hosting.service`, `deploy/scripts/nginx-itest.sh`
- Test: the script itself (needs `nginx`, `curl`, `openssl` installed; skip with a clear message if absent)

**Interfaces:**
- Consumes: the file layout (`/var/lib/report_hosting/by-slug/{slug}/current`, `.htpasswd`), the Go binary's local listen address.
- Produces:
  - `report_hosting.conf` with four server blocks: admin (exact name, proxy to Go, `frame-ancestors 'none'` and `X-Frame-Options`, `client_max_body_size 55m`, `limit_req` 10 r/m for deploy), hosted sites (regex `~^(?<slug>[a-z0-9-]+)\.partnersflex\.space$`, `root .../by-slug/$slug/current`, `auth_basic_user_file .../by-slug/$slug/.htpasswd`, `disable_symlinks on from=$document_root`, 404 check before auth with `if (!-d ...)`, `Cache-Control: private, no-store`, `limit_req` 30 r/s burst 60, hidden-file deny, TLS 1.2/1.3), a default server for unknown hosts, `ssl_verify_client on` with `ssl_client_certificate` (zone custom CA), `set_real_ip_from` for Cloudflare IPv4 and IPv6 plus `real_ip_header CF-Connecting-IP`
  - `report_hosting.service` per §12.2: `User=report_hosting`, `NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `ReadWritePaths=/var/lib/report_hosting`, `EnvironmentFile` mode 0600, `Restart=on-failure`

- [ ] **Step 1: Write the failing integration script.** It builds a temp tree with two sites and a self-signed cert, starts Nginx with the config (paths templated), and asserts with `curl --resolve`:
  ```text
  correct password -> 200 (AC-06, AC-27); wrong password -> 401; site A password on site B -> 401 (AC-07)
  repeat request without credentials after an authorised one -> 401 (AC-16)
  unknown slug and disabled (link removed) -> 404, error log has no "No such file" for htpasswd (AC-10, AC-17)
  create a new by-slug link while Nginx runs -> reachable with NO reload (AC-13)
  request for /.htpasswd and /.git/x -> 404/403
  response has Cache-Control: private, no-store; admin response has X-Frame-Options: DENY (AC-23)
  new release dir created by the service user with umask 027 is readable by nginx group -> 200 (not 403)
  connection without client certificate -> rejected (AC-26 local analog)
  nginx -t passes
  ```
- [ ] **Step 2: Run** `bash deploy/scripts/nginx-itest.sh` → FAIL (no config)
- [ ] **Step 3: Write `report_hosting.conf` and `report_hosting.service`.** Placeholders for domain, cert paths, data dir.
- [ ] **Step 4: Run** the script → all assertions print PASS
- [ ] **Step 5: Commit** `feat: nginx config, systemd unit, integration test`

### Task 13: Runbook and production verification (Phase 1, can start in parallel)

**Files:**
- Create: `deploy/RUNBOOK.md`

**Interfaces:** consumes the files from Task 12; produces a manual checklist. No code.

- [ ] **Step 1: Write the runbook** with these ordered, checkable items (each with the exact command or console path and its expected result):
  1. Add `partnersflex.space` to Cloudflare; point the registrar nameservers; wait for Active.
  2. DNS: `A *` → VPS IP, proxied. Confirm **no change** in the `visionp.uk` zone (record export before and after, diff is empty). (AC-14)
  3. SSL/TLS mode Full (strict); Origin Certificate for `partnersflex.space` and `*.partnersflex.space` installed on the VPS.
  4. Zone-level Authenticated Origin Pulls with a **custom** client certificate; install its CA for Nginx. Shared Cloudflare certificate must be rejected. (AC-26)
  5. Cache Rule: bypass cache for `*.partnersflex.space`. (AC-16)
  6. Cloudflare Access application on `admin.partnersflex.space`; copy team domain and AUD into the config. (AC-01, AC-28)
  7. VPS: user/group `report_hosting`, directories and modes (`db/`, `staging/` 0700; others 0750), install config and unit, `nginx -t` then reload, `systemctl enable --now report_hosting`, `report_hosting admin create`.
  8. Verify existing `visionp.uk` services still answer (AC-14).
  9. Smoke test: sign in, create site, upload ZIP and HTML, open the site with the generated password, disable, enable, delete.
- [ ] **Step 2: Execute the checklist on the VPS** and record each result in the runbook. Expected: every box checked; any failure blocks release.
- [ ] **Step 3: Commit** `docs: deployment runbook`

### Task 14: Hardening and acceptance run

**Files:**
- Create: `deploy/scripts/acceptance.sh` (live checks for AC-01…AC-29 that can be scripted against the real host), `internal/service/acceptance_test.go` (cross-package scenarios)
- Modify: whichever task's code a failing scenario exposes

**Interfaces:** consumes everything above; produces the traceability table below.

- [ ] **Step 1: Write failing scenario tests** that drive the service layer end to end:
  ```go
  TestEndToEndLifecycle: create -> deploy ZIP -> deploy HTML -> rename -> disable -> enable -> regenerate -> delete; at each step assert link, htpasswd, DB, files
  TestRestartRecoveryScenario: kill after symlink switch, run Recover, site serves the new release
  TestOtherSitesUntouched: operations on site A never change site B's link, files, credentials   // AC-12
  TestNoSecretsInAuditOrLogs
  ```
- [ ] **Step 2: Run** `go test ./... -race -count=1` → FAIL on anything not yet wired
- [ ] **Step 3: Fix** gaps in the owning task's code; do not paper over them in the test.
- [ ] **Step 4: Run** `go vet ./... && go test ./... -race -count=1 && bash deploy/scripts/nginx-itest.sh` → all PASS; then run `acceptance.sh` against the VPS.
- [ ] **Step 5: Commit** `test: acceptance scenarios and hardening fixes`

---

## Spec Traceability

| Spec item | Task |
| --- | --- |
| FR-01, AC-25 | 8, 9, 10 |
| FR-02/03, AC-02/03 | 1, 7 |
| FR-04/05/06/07, AC-04/05/09/18/19 | 4, 5 |
| FR-08/09, AC-21/24/27 | 1, 3, 7 |
| FR-10, AC-08/20 | 5 |
| FR-11 | 10, 11 |
| FR-12, AC-11 | 6, 7 |
| FR-13, AC-12/17 | 7, 10, 11 |
| FR-14, AC-10/13 | 12 |
| FR-15 | 5 |
| AC-01, AC-14, AC-16, AC-26, AC-28 | 9, 12, 13 |
| AC-22 | 7 |
| AC-23 | 9, 12 |
| AC-29 | 6, 14 |
| AC-15 (backups) | Deferred (§12.3) |

## Self-Review Notes

- **Spec coverage:** every FR and AC maps to a task above; AC-15 is deliberately deferred.
- **Spec fixes made while planning:** `releases.status` gained `superseded` (otherwise recovery would delete retained releases); an `admins` table was added.
- **Plan decisions the spec left open:** Go 1.22+, JSON config with `REPORT_HOSTING_*` env secrets, JWT library, per-site mutex for deploy serialization, password shown only in the create and regenerate responses.
- **Not covered by code:** Cloudflare console steps (Task 13 is manual) and the real VPS Nginx build; both are checked by the runbook and `acceptance.sh`.
