package repository

import (
	"context"
	"time"

	"report_hosting/internal/domain"
)

func (s *Store) CreateSite(ctx context.Context, v domain.Site) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sites("+siteColumns+") VALUES(?,?,?,?,?,?,?,?)",
		v.ID, v.Name, v.Slug, v.AuthUsername, v.Status, v.CurrentReleaseID,
		v.CreatedAt.UnixNano(), v.UpdatedAt.UnixNano())
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
	return changed(s.db.ExecContext(ctx,
		"UPDATE sites SET name=?,slug=?,updated_at=? WHERE id=?",
		name, slug, time.Now().UnixNano(), id))
}
func (s *Store) SetSiteStatus(ctx context.Context, id string, status domain.SiteStatus) error {
	return changed(s.db.ExecContext(ctx,
		"UPDATE sites SET status=?,updated_at=? WHERE id=?",
		status, time.Now().UnixNano(), id))
}
func (s *Store) SetAuthUsername(ctx context.Context, id, username string) error {
	return changed(s.db.ExecContext(ctx,
		"UPDATE sites SET auth_username=?,updated_at=? WHERE id=?",
		username, time.Now().UnixNano(), id))
}
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id))
}

// DeleteSiteWithAudit commits the deletion and its durable audit event together.
// Files are removed by the service only after this transaction succeeds.
func (s *Store) DeleteSiteWithAudit(ctx context.Context, id, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		"INSERT INTO audit_events(actor,action,site_id,created_at) VALUES(?,'delete',?,?)",
		actor, id, time.Now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
