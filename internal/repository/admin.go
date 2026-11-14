package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) CreateAdmin(ctx context.Context, username, hash string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO admins(id,username,password_hash,created_at) VALUES(1,?,?,?)", username, hash, time.Now().UnixNano())
	return err
}

// ReplaceAdmin swaps the single administrator, ends every existing session and
// records the change. All writes commit together or not at all.
func (s *Store) ReplaceAdmin(ctx context.Context, username, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixNano()
	if _, err = tx.ExecContext(ctx, "DELETE FROM admins"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO admins(id,username,password_hash,created_at) VALUES(1,?,?,?)", username, hash, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,site_id,created_at) VALUES('cli','admin_credentials','',?)", now); err != nil {
		return err
	}
	return tx.Commit()
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
