package repository

import (
	"context"
	"time"
)

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
