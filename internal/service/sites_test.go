package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/storage"
	"strings"
	"testing"
	"time"
)

func testSites(t *testing.T) *Sites {
	t.Helper()
	root := t.TempDir()
	layout := storage.Layout{Root: root}
	if err := layout.Init(); err != nil {
		t.Fatal(err)
	}
	st, err := repository.Open(filepath.Join(root, "db", "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := storage.NewDeployer(layout, st, storage.Limits{MaxZipBytes: 1024 * 1024, MaxExtractedBytes: 1024 * 1024, MaxFiles: 100, MaxRatio: 100}, 0)
	return NewSites(st, layout, routing.Links{Dir: layout.BySlugDir()}, d, []string{"admin"})
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestSiteLifecycleAndDisabledRename(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	v, pw, err := s.Create(ctx, "admin", "Demo", "demo")
	check(t, err)
	if len(pw) < 20 {
		t.Fatal("short password")
	}
	line, err := os.ReadFile(s.Layout.HtpasswdPath(v.ID))
	check(t, err)
	if !storage.VerifySSHA(string(line), pw) {
		t.Fatal("invalid credentials")
	}
	r, err := s.DeployHTML(ctx, "admin", v.ID, strings.NewReader("<h1>one</h1>"))
	check(t, err)
	if r.Version != 1 {
		t.Fatal(r)
	}
	if err = s.Delete(ctx, "admin", v.ID, "demo"); !errors.Is(err, domain.ErrSiteNotDisabled) {
		t.Fatal(err)
	}
	check(t, s.Disable(ctx, "admin", v.ID))
	check(t, s.Rename(ctx, "admin", v.ID, "Renamed", "renamed"))
	links, err := s.Links.List()
	check(t, err)
	if len(links) != 0 {
		t.Fatalf("disabled site reopened: %v", links)
	}
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	links, err = s.Links.List()
	check(t, err)
	if len(links) != 0 {
		t.Fatal(links)
	}
	check(t, s.Enable(ctx, "admin", v.ID))
	pw2, err := s.RegenerateCredentials(ctx, "admin", v.ID, "new-user")
	check(t, err)
	line, err = os.ReadFile(s.Layout.HtpasswdPath(v.ID))
	check(t, err)
	if storage.VerifySSHA(string(line), pw) || !storage.VerifySSHA(string(line), pw2) {
		t.Fatal("password rotation failed")
	}
	check(t, s.Disable(ctx, "admin", v.ID))
	if err = s.Delete(ctx, "admin", v.ID, "wrong"); !errors.Is(err, domain.ErrSlugConfirmation) {
		t.Fatal(err)
	}
	check(t, s.Delete(ctx, "admin", v.ID, "renamed"))
	if _, err = os.Stat(s.Layout.SiteDir(v.ID)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	audit, err := s.Store.ListAudit(ctx, v.ID)
	check(t, err)
	if len(audit) < 8 {
		t.Fatal(audit)
	}
	for _, e := range audit {
		if strings.Contains(e.Action, pw2) {
			t.Fatal("secret in audit")
		}
	}
}
func TestRecoverySwitchedReleaseAndCommittedDisable(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	v, _, err := s.Create(ctx, "admin", "Demo", "demo")
	check(t, err)
	_, err = s.DeployHTML(ctx, "admin", v.ID, strings.NewReader("one"))
	check(t, err)
	dest := s.Layout.ReleaseDir(v.ID, 2)
	check(t, os.MkdirAll(dest, 0750))
	check(t, os.WriteFile(filepath.Join(dest, "index.html"), []byte("two"), 0640))
	check(t, s.Store.AddRelease(ctx, domain.Release{ID: "v2", SiteID: v.ID, Version: 2, StoragePath: dest, Checksum: "x", Status: domain.ReleaseReady, CreatedAt: time.Now()}))
	temp := filepath.Join(s.Layout.SiteDir(v.ID), "switch")
	check(t, os.Symlink(filepath.Join("releases", "000002"), temp))
	check(t, os.Rename(temp, filepath.Join(s.Layout.SiteDir(v.ID), "current")))
	check(t, s.Store.SetSiteStatus(ctx, v.ID, domain.SiteDisabled))
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	got, err := s.Get(ctx, v.ID)
	check(t, err)
	if got.CurrentReleaseID != "v2" || got.Status != domain.SiteDisabled {
		t.Fatal(got)
	}
	links, err := s.Links.List()
	check(t, err)
	if len(links) != 0 {
		t.Fatal("disable reopened")
	}
	rs, err := s.Releases(ctx, v.ID)
	check(t, err)
	if rs[0].Status != domain.ReleaseActive || rs[1].Status != domain.ReleaseSuperseded {
		t.Fatal(rs)
	}
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
}
func TestRecoveryRemovesReadyAndOrphanKeepsOtherSite(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	a, _, err := s.Create(ctx, "admin", "A", "a")
	check(t, err)
	b, _, err := s.Create(ctx, "admin", "B", "b")
	check(t, err)
	dest := s.Layout.ReleaseDir(a.ID, 1)
	check(t, os.MkdirAll(dest, 0750))
	check(t, s.Store.AddRelease(ctx, domain.Release{ID: "ready", SiteID: a.ID, Version: 1, StoragePath: dest, Status: domain.ReleaseReady, CreatedAt: time.Now()}))
	orphan := s.Layout.ReleaseDir(a.ID, 2)
	check(t, os.MkdirAll(orphan, 0750))
	check(t, s.Links.Create("stale", a.ID))
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	for _, p := range []string{dest, orphan} {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("orphan retained", p)
		}
	}
	links, err := s.Links.List()
	check(t, err)
	if len(links) != 2 || links["b"] != b.ID {
		t.Fatal(links)
	}
	check(t, s.Disable(ctx, "admin", a.ID))
	check(t, s.Delete(ctx, "admin", a.ID, "a"))
	_, err = s.Get(ctx, b.ID)
	check(t, err)
}

func TestDeployLockIsPerSite(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	a, _, err := s.Create(ctx, "admin", "A", "site-a")
	check(t, err)
	b, _, err := s.Create(ctx, "admin", "B", "site-b")
	check(t, err)
	unlock := s.Deployer.LockSite(a.ID) // simulates an in-flight deploy of A
	done := make(chan error, 1)
	go func() { done <- s.Rename(ctx, "admin", b.ID, "B2", "site-b2") }()
	select {
	case err := <-done:
		check(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("operation on another site blocked by a deploy of site A")
	}
	check(t, s.Disable(ctx, "admin", a.ID))
	del := make(chan error, 1)
	go func() { del <- s.Delete(ctx, "admin", a.ID, "site-a") }()
	select {
	case <-del:
		t.Fatal("Delete ran while a deploy held the site lock")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-del:
		check(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Delete did not proceed after the deploy finished")
	}
}
