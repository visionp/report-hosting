// Package repository persists metadata, authentication state and audit events.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"report_hosting/internal/domain"
	"report_hosting/migrations"
)

type Store struct{ db *sql.DB }
type Admin struct {
	Username, PasswordHash string
	CreatedAt              time.Time
}
type Session struct {
	IDHash                           string
	CreatedAt, LastSeenAt, ExpiresAt time.Time
}
type AuditEvent struct {
	ID                    int64
	Actor, Action, SiteID string
	CreatedAt             time.Time
}

// Open initializes the schema. A single connection keeps pragmas and writes consistent.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = tx.Exec(migrations.Initial); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func mapped(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: sites.slug") {
		return domain.ErrDuplicateSlug
	}
	return err
}
func changed(r sql.Result, err error) error {
	if err != nil {
		return mapped(err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const siteColumns = "id,name,slug,auth_username,status,current_release_id,created_at,updated_at"

type scanner interface{ Scan(...any) error }

func scanSite(r scanner) (domain.Site, error) {
	var v domain.Site
	var c, u int64
	err := r.Scan(&v.ID, &v.Name, &v.Slug, &v.AuthUsername, &v.Status, &v.CurrentReleaseID, &c, &u)
	v.CreatedAt = time.Unix(0, c).UTC()
	v.UpdatedAt = time.Unix(0, u).UTC()
	return v, mapped(err)
}
func (s *Store) CreateSite(ctx context.Context, v domain.Site) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO sites("+siteColumns+") VALUES(?,?,?,?,?,?,?,?)", v.ID, v.Name, v.Slug, v.AuthUsername, v.Status, v.CurrentReleaseID, v.CreatedAt.UnixNano(), v.UpdatedAt.UnixNano())
	return mapped(err)
}
func (s *Store) GetSite(ctx context.Context, id string) (domain.Site, error) {
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", id))
}
func (s *Store) GetSiteBySlug(ctx context.Context, slug string) (domain.Site, error) {
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE slug=?", slug))
}
func (s *Store) ListSites(ctx context.Context) ([]domain.Site, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+siteColumns+" FROM sites ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Site{}
	for rows.Next() {
		v, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSite(ctx context.Context, id, name, slug string) error {
	return changed(s.db.ExecContext(ctx, "UPDATE sites SET name=?,slug=?,updated_at=? WHERE id=?", name, slug, time.Now().UnixNano(), id))
}
func (s *Store) SetSiteStatus(ctx context.Context, id string, status domain.SiteStatus) error {
	return changed(s.db.ExecContext(ctx, "UPDATE sites SET status=?,updated_at=? WHERE id=?", status, time.Now().UnixNano(), id))
}
func (s *Store) SetAuthUsername(ctx context.Context, id, username string) error {
	return changed(s.db.ExecContext(ctx, "UPDATE sites SET auth_username=?,updated_at=? WHERE id=?", username, time.Now().UnixNano(), id))
}
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id))
}

const releaseColumns = "id,site_id,version,storage_path,checksum,status,created_at"

func scanRelease(r scanner) (domain.Release, error) {
	var v domain.Release
	var c int64
	err := r.Scan(&v.ID, &v.SiteID, &v.Version, &v.StoragePath, &v.Checksum, &v.Status, &c)
	v.CreatedAt = time.Unix(0, c).UTC()
	return v, mapped(err)
}
func (s *Store) AddRelease(ctx context.Context, v domain.Release) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO releases("+releaseColumns+") VALUES(?,?,?,?,?,?,?)", v.ID, v.SiteID, v.Version, v.StoragePath, v.Checksum, v.Status, v.CreatedAt.UnixNano())
	return err
}
func (s *Store) releases(ctx context.Context, where string, arg any) ([]domain.Release, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+releaseColumns+" FROM releases WHERE "+where+" ORDER BY version DESC,id", arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Release{}
	for rows.Next() {
		v, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ListReleases(ctx context.Context, siteID string) ([]domain.Release, error) {
	return s.releases(ctx, "site_id=?", siteID)
}
func (s *Store) ListReleasesByStatus(ctx context.Context, status domain.ReleaseStatus) ([]domain.Release, error) {
	return s.releases(ctx, "status=?", status)
}

// ActivateRelease validates site ownership and updates both records in one transaction.
func (s *Store) ActivateRelease(ctx context.Context, siteID, releaseID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var found string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM releases WHERE id=? AND site_id=?", releaseID, siteID).Scan(&found); err != nil {
		return mapped(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE releases SET status='superseded' WHERE site_id=? AND status='active'", siteID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE releases SET status='active' WHERE id=?", releaseID); err != nil {
		return err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE sites SET current_release_id=?,updated_at=? WHERE id=?", releaseID, time.Now().UnixNano(), siteID)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) DeleteRelease(ctx context.Context, id string) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM releases WHERE id=? AND status!='active' AND NOT EXISTS(SELECT 1 FROM sites WHERE current_release_id=?)", id, id))
}

// NextVersion must be called under the site's deployment lock through AddRelease.
func (s *Store) NextVersion(ctx context.Context, siteID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM releases WHERE site_id=?", siteID).Scan(&n)
	return n, err
}
func (s *Store) CreateAdmin(ctx context.Context, username, hash string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO admins(id,username,password_hash,created_at) VALUES(1,?,?,?)", username, hash, time.Now().UnixNano())
	return err
}
func (s *Store) GetAdmin(ctx context.Context, username string) (Admin, error) {
	var v Admin
	var c int64
	err := s.db.QueryRowContext(ctx, "SELECT username,password_hash,created_at FROM admins WHERE username=?", username).Scan(&v.Username, &v.PasswordHash, &c)
	v.CreatedAt = time.Unix(0, c).UTC()
	return v, mapped(err)
}
func (s *Store) PutSession(ctx context.Context, idHash string, created, lastSeen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO admin_sessions(id_hash,created_at,last_seen_at,expires_at) VALUES(?,?,?,?)", idHash, created.UnixNano(), lastSeen.UnixNano(), expires.UnixNano())
	return err
}
func (s *Store) GetSession(ctx context.Context, idHash string) (Session, error) {
	var v Session
	var c, l, e int64
	err := s.db.QueryRowContext(ctx, "SELECT id_hash,created_at,last_seen_at,expires_at FROM admin_sessions WHERE id_hash=?", idHash).Scan(&v.IDHash, &c, &l, &e)
	v.CreatedAt = time.Unix(0, c).UTC()
	v.LastSeenAt = time.Unix(0, l).UTC()
	v.ExpiresAt = time.Unix(0, e).UTC()
	return v, mapped(err)
}
func (s *Store) TouchSession(ctx context.Context, idHash string, now time.Time) error {
	return changed(s.db.ExecContext(ctx, "UPDATE admin_sessions SET last_seen_at=MAX(last_seen_at,?) WHERE id_hash=?", now.UnixNano(), idHash))
}
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE id_hash=?", idHash)
	return err
}

// RecordFailure increments atomically and starts a fresh window when the old one expires.
func (s *Store) RecordFailure(ctx context.Context, key string, now time.Time, window time.Duration) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `INSERT INTO login_attempts(key,count,window_start,locked_until) VALUES(?,1,?,0)
 ON CONFLICT(key) DO UPDATE SET count=CASE WHEN login_attempts.window_start<=? THEN 1 ELSE login_attempts.count+1 END,
 window_start=CASE WHEN login_attempts.window_start<=? THEN excluded.window_start ELSE login_attempts.window_start END RETURNING count`, key, now.UnixNano(), now.Add(-window).UnixNano(), now.Add(-window).UnixNano()).Scan(&n)
	return n, err
}
func (s *Store) Lock(ctx context.Context, key string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO login_attempts(key,count,window_start,locked_until) VALUES(?,0,?,?) ON CONFLICT(key) DO UPDATE SET locked_until=MAX(login_attempts.locked_until,excluded.locked_until)", key, time.Now().UnixNano(), until.UnixNano())
	return err
}
func (s *Store) LockedUntil(ctx context.Context, key string) (time.Time, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT locked_until FROM login_attempts WHERE key=?", key).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) || n == 0 {
		return time.Time{}, errUnlessNoRows(err)
	}
	return time.Unix(0, n).UTC(), err
}
func errUnlessNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
func (s *Store) ClearFailures(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM login_attempts WHERE key=?", key)
	return err
}

// PurgeStale removes login_attempts rows whose window and lock have both
// ended, and sessions past their absolute expiry or idle timeout.
func (s *Store) PurgeStale(ctx context.Context, now time.Time, window, idle time.Duration) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM login_attempts WHERE locked_until<=? AND window_start<=?", now.UnixNano(), now.Add(-window).UnixNano()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at<=? OR last_seen_at<=?", now.UnixNano(), now.Add(-idle).UnixNano())
	return err
}
func (s *Store) AddAudit(ctx context.Context, actor, action, siteID string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO audit_events(actor,action,site_id,created_at) VALUES(?,?,?,?)", actor, action, siteID, time.Now().UnixNano())
	return err
}
func (s *Store) ListAudit(ctx context.Context, siteID string) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,actor,action,site_id,created_at FROM audit_events WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var v AuditEvent
		var c int64
		if err = rows.Scan(&v.ID, &v.Actor, &v.Action, &v.SiteID, &c); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(0, c).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
