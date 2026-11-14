package repository

import (
	"context"
	"time"

	"report_hosting/internal/domain"
)

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
