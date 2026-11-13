package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
)

func deployment(t *testing.T) (*Deployer, *repository.Store) {
	t.Helper()
	layout := Layout{Root: t.TempDir()}
	if err := layout.Init(); err != nil {
		t.Fatal(err)
	}
	// Windows installations without developer mode cannot create symbolic links.
	probe := filepath.Join(layout.Root, "probe")
	if err := os.Symlink("missing", probe); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	os.Remove(probe)
	st, err := repository.Open(filepath.Join(layout.Root, "db", "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSite(context.Background(), domain.Site{ID: "site-id", Name: "Site", Slug: "site", AuthUsername: "site", Status: domain.SiteActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d := NewDeployer(layout, st, limits(), 1<<20)
	return d, st
}

func TestDeployLifecycleRetentionAndFailure(t *testing.T) {
	d, st := deployment(t)
	ctx := context.Background()
	first, err := d.DeployZip(ctx, "site-id", archive(t, entry{"index.html", "first", 0}))
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Status != domain.ReleaseActive || len(first.Checksum) != 64 {
		t.Fatal(first)
	}
	if _, err := d.DeployZip(ctx, "site-id", archive(t, entry{"../bad", "bad", 0})); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.Layout.SiteDir("site-id"), "current", "index.html"))
	if err != nil || string(b) != "first" {
		t.Fatal("failed deploy changed site", err)
	}
	for i := 2; i <= 5; i++ {
		r, err := d.DeployHTML(ctx, "site-id", strings.NewReader("next"))
		if err != nil {
			t.Fatal(err)
		}
		if r.Version != i {
			t.Fatal(r.Version)
		}
	}
	releases, err := st.ListReleases(ctx, "site-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 3 {
		t.Fatal(releases)
	}
	if releases[0].Version != 5 || releases[0].Status != domain.ReleaseActive || releases[1].Status != domain.ReleaseSuperseded {
		t.Fatal(releases)
	}
	if _, err := os.Stat(d.Layout.ReleaseDir("site-id", 1)); !os.IsNotExist(err) {
		t.Fatal("old release retained")
	}
	entries, err := os.ReadDir(d.Layout.StagingDir())
	if err != nil || len(entries) != 0 {
		t.Fatal("staging leaked", err)
	}
	stt, err := os.Stat(d.Layout.ReleaseDir("site-id", 5))
	if err != nil {
		t.Fatal(err)
	}
	// Directory root is initially made by MkdirTemp, and must be opened to Nginx.
	if stt.Mode().Perm()&0050 != 0050 {
		t.Fatal(stt.Mode())
	}
}

func TestDeploySerializesConcurrentUploads(t *testing.T) {
	d, st := deployment(t)
	ctx := context.Background()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.DeployHTML(ctx, "site-id", strings.NewReader("complete"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := st.ListReleases(ctx, "site-id")
	if err != nil || len(r) != 2 || r[0].Version != 2 || r[1].Version != 1 {
		t.Fatal(r, err)
	}
}

func TestDeployDiskSpaceRejectedBeforeReading(t *testing.T) {
	d, _ := deployment(t)
	d.freeSpace = func(string) (uint64, error) { return 1, nil }
	if _, err := d.DeployHTML(context.Background(), "site-id", strings.NewReader("hello")); !errors.Is(err, ErrInsufficientDisk) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(d.Layout.StagingDir())
	if len(entries) != 0 {
		t.Fatal("staging allocated before disk check")
	}
}

func TestDeployCrashPreservesReadyCurrent(t *testing.T) {
	d, st := deployment(t)
	ctx := context.Background()
	if _, err := d.DeployHTML(ctx, "site-id", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("simulated crash")
	d.afterSwitch = func() error { return crash }
	r, err := d.DeployHTML(ctx, "site-id", strings.NewReader("new"))
	if !errors.Is(err, crash) {
		t.Fatal(err)
	}
	site, err := st.GetSite(ctx, "site-id")
	if err != nil {
		t.Fatal(err)
	}
	if site.CurrentReleaseID == r.ID {
		t.Fatal("database already switched")
	}
	b, err := os.ReadFile(filepath.Join(d.Layout.SiteDir("site-id"), "current", "index.html"))
	if err != nil || string(b) != "new" {
		t.Fatal(string(b), err)
	}
	releases, err := st.ListReleases(ctx, "site-id")
	if err != nil || releases[0].Status != domain.ReleaseReady {
		t.Fatal(releases, err)
	}
}
