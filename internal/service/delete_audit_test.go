package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteAuditFailurePreservesSiteAndFiles(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	v, _, err := s.Create(ctx, "admin", "Demo", "demo")
	check(t, err)
	_, err = s.DeployHTML(ctx, "admin", v.ID, strings.NewReader("keep this release"))
	check(t, err)
	check(t, s.Disable(ctx, "admin", v.ID))
	db, err := sql.Open("sqlite", filepath.Join(s.Layout.Root, "db", "test.sqlite"))
	check(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events WHEN NEW.action='delete' BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`)
	check(t, err)
	if err = s.Delete(ctx, "admin", v.ID, "demo"); err == nil {
		t.Fatal("expected audit failure")
	}
	_, err = s.Get(ctx, v.ID)
	check(t, err)
	body, err := os.ReadFile(filepath.Join(s.Layout.SiteDir(v.ID), "current", "index.html"))
	check(t, err)
	if string(body) != "keep this release" {
		t.Fatal("files changed after rollback")
	}
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	links, err := s.Links.List()
	check(t, err)
	if len(links) != 0 {
		t.Fatal("failed deletion reopened disabled site")
	}
	_, err = db.Exec("DROP TRIGGER fail_audit")
	check(t, err)
	check(t, s.Delete(ctx, "admin", v.ID, "demo"))
	audit, err := s.Store.ListAudit(ctx, v.ID)
	check(t, err)
	count := 0
	for _, event := range audit {
		if event.Action == "delete" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("delete audit count: %d", count)
	}
	if _, err = os.Stat(s.Layout.SiteDir(v.ID)); !os.IsNotExist(err) {
		t.Fatalf("files retained after success: %v", err)
	}
}
