// Package repository is the SQLite adapter for sites, releases, administrator
// sessions, and the audit log.
package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
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
	// Pragmas live in the DSN so every (re)opened pooled connection gets them.
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	dsn := (&url.URL{Scheme: "file", Opaque: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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
