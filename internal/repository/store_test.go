package repository

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"report_hosting/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func site(id, slug string) domain.Site {
	now := time.Now().UTC()
	return domain.Site{ID: id, Name: slug, Slug: slug, AuthUsername: slug, Status: domain.SiteStatus("active"), CreatedAt: now, UpdatedAt: now}
}
func release(id, siteID string, n int) domain.Release {
	return domain.Release{ID: id, SiteID: siteID, Version: n, StoragePath: "releases/" + id, Checksum: "digest", Status: domain.ReleaseStatus("ready"), CreatedAt: time.Now().UTC()}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSiteLifecycleAndDuplicateSlug(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := site("a", "demo")
	must(t, s.CreateSite(ctx, a))
	if err := s.CreateSite(ctx, site("b", "demo")); !errors.Is(err, domain.ErrDuplicateSlug) {
		t.Fatalf("duplicate: %v", err)
	}
	got, err := s.GetSiteBySlug(ctx, "demo")
	must(t, err)
	if got.ID != a.ID || !got.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("roundtrip: %+v", got)
	}
	must(t, s.CreateSite(ctx, site("b", "other")))
	if err = s.UpdateSite(ctx, "a", "renamed", "other"); !errors.Is(err, domain.ErrDuplicateSlug) {
		t.Fatalf("rename duplicate: %v", err)
	}
	must(t, s.UpdateSite(ctx, "a", "Renamed", "new"))
	must(t, s.SetAuthUsername(ctx, "a", "user"))
	must(t, s.SetSiteStatus(ctx, "a", domain.SiteStatus("disabled")))
	got, err = s.GetSite(ctx, "a")
	must(t, err)
	if got.Slug != "new" || got.Name != "Renamed" || got.AuthUsername != "user" || got.Status != "disabled" {
		t.Fatalf("updated %+v", got)
	}
	list, err := s.ListSites(ctx)
	must(t, err)
	if len(list) != 2 {
		t.Fatal(list)
	}
	if err = s.DeleteSite(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestReleaseActivationIsolationAndCascade(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.CreateSite(ctx, site("a", "a")))
	must(t, s.CreateSite(ctx, site("b", "b")))
	for _, id := range []string{"a", "b"} {
		n, err := s.NextVersion(ctx, id)
		must(t, err)
		if n != 1 {
			t.Fatal(n)
		}
	}
	must(t, s.AddRelease(ctx, release("v1", "a", 1)))
	must(t, s.AddRelease(ctx, release("v2", "a", 2)))
	must(t, s.AddRelease(ctx, release("bv1", "b", 1)))
	must(t, s.ActivateRelease(ctx, "a", "v1"))
	if err := s.ActivateRelease(ctx, "a", "bv1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-site activation: %v", err)
	}
	got, err := s.GetSite(ctx, "a")
	must(t, err)
	if got.CurrentReleaseID != "v1" {
		t.Fatal(got)
	}
	must(t, s.ActivateRelease(ctx, "a", "v2"))
	rs, err := s.ListReleases(ctx, "a")
	must(t, err)
	if len(rs) != 2 || rs[0].ID != "v2" || rs[0].Status != "active" || rs[1].Status != "superseded" {
		t.Fatal(rs)
	}
	if err = s.DeleteRelease(ctx, "v2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("active deletion: %v", err)
	}
	ready, err := s.ListReleasesByStatus(ctx, domain.ReleaseStatus("ready"))
	must(t, err)
	if len(ready) != 1 || ready[0].ID != "bv1" {
		t.Fatal(ready)
	}
	must(t, s.AddAudit(ctx, "admin", "delete", "a"))
	must(t, s.DeleteSite(ctx, "a"))
	rs, err = s.ListReleases(ctx, "a")
	must(t, err)
	if len(rs) != 0 {
		t.Fatal(rs)
	}
	audit, err := s.ListAudit(ctx, "a")
	must(t, err)
	if len(audit) != 1 {
		t.Fatal(audit)
	}
	_, err = s.GetSite(ctx, "b")
	must(t, err)
}
func TestSessionAndSingleAdmin(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.CreateAdmin(ctx, "admin", "hash"))
	if err := s.CreateAdmin(ctx, "other", "hash2"); err == nil {
		t.Fatal("second admin accepted")
	}
	a, err := s.GetAdmin(ctx, "admin")
	must(t, err)
	if a.PasswordHash != "hash" {
		t.Fatal(a)
	}
	now := time.Now().UTC()
	must(t, s.PutSession(ctx, "id-hash", now, now, now.Add(12*time.Hour)))
	must(t, s.TouchSession(ctx, "id-hash", now.Add(time.Minute)))
	v, err := s.GetSession(ctx, "id-hash")
	must(t, err)
	if !v.CreatedAt.Equal(now) || !v.LastSeenAt.Equal(now.Add(time.Minute)) || !v.ExpiresAt.Equal(now.Add(12*time.Hour)) {
		t.Fatal(v)
	}
	must(t, s.TouchSession(ctx, "id-hash", now))
	v, err = s.GetSession(ctx, "id-hash")
	must(t, err)
	if !v.LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatal("last_seen went backwards")
	}
	must(t, s.DeleteSession(ctx, "id-hash"))
	_, err = s.GetSession(ctx, "id-hash")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestFailedLoginCountersAtomicAndExpire(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.RecordFailure(ctx, "account:a", now, 15*time.Minute); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	n, err := s.RecordFailure(ctx, "account:a", now, 15*time.Minute)
	must(t, err)
	if n != 21 {
		t.Fatal(n)
	}
	n, err = s.RecordFailure(ctx, "account:a", now.Add(15*time.Minute), 15*time.Minute)
	must(t, err)
	if n != 1 {
		t.Fatal(n)
	}
	until := now.Add(30 * time.Minute)
	must(t, s.Lock(ctx, "account:a", until))
	must(t, s.Lock(ctx, "account:a", now))
	got, err := s.LockedUntil(ctx, "account:a")
	must(t, err)
	if !got.Equal(until) {
		t.Fatal(got)
	}
	must(t, s.ClearFailures(ctx, "account:a"))
	got, err = s.LockedUntil(ctx, "account:a")
	must(t, err)
	if !got.IsZero() {
		t.Fatal(got)
	}
}
func TestReopenAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.sqlite")
	s, err := Open(path)
	must(t, err)
	must(t, s.CreateSite(context.Background(), site("a", "a")))
	must(t, s.Close())
	s, err = Open(path)
	must(t, err)
	defer s.Close()
	_, err = s.GetSite(context.Background(), "a")
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.ListSites(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
