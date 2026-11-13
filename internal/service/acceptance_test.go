package service

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"report_hosting/internal/domain"
	"strings"
	"sync"
	"testing"
)

func TestServiceLogsExcludeSecretsAndUploadPaths(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	var out bytes.Buffer
	s.Logger = slog.New(slog.NewJSONHandler(&out, nil))
	v, pw, err := s.Create(ctx, "admin", "A", "a")
	check(t, err)
	_, err = s.DeployHTML(ctx, "admin", v.ID, strings.NewReader("<h1>private-content</h1>"))
	check(t, err)
	secretPath := filepath.Join(t.TempDir(), "private-upload.zip")
	_, err = s.DeployZip(ctx, "admin", v.ID, secretPath)
	if err == nil {
		t.Fatal("missing upload accepted")
	}
	check(t, s.Disable(ctx, "admin", v.ID))
	text := out.String()
	for _, secret := range []string{pw, "private-content", secretPath, s.Layout.Root, "Authorization", "Cf-Access-Jwt-Assertion"} {
		if strings.Contains(text, secret) {
			t.Fatal("secret in logs", secret)
		}
	}
	for _, event := range []string{"create", "upload", "deploy", "disable"} {
		if !strings.Contains(text, event) {
			t.Fatal("missing event", event)
		}
	}
}

func TestAcceptanceLifecycleIsolationAndConcurrentDeploy(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	a, pw, err := s.Create(ctx, "admin", "A", "a")
	check(t, err)
	b, _, err := s.Create(ctx, "admin", "B", "b")
	check(t, err)
	bCred, err := os.ReadFile(s.Layout.HtpasswdPath(b.ID))
	check(t, err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("index.html")
	check(t, err)
	_, err = f.Write([]byte("ZIP"))
	check(t, err)
	check(t, zw.Close())
	zipPath := filepath.Join(t.TempDir(), "site.zip")
	check(t, os.WriteFile(zipPath, buf.Bytes(), 0600))
	_, err = s.DeployZip(ctx, "admin", a.ID, zipPath)
	check(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DeployHTML(ctx, "admin", a.ID, strings.NewReader("complete"))
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rs, err := s.Releases(ctx, a.ID)
	check(t, err)
	if len(rs) != 3 || rs[0].Version != 5 || rs[0].Status != domain.ReleaseActive {
		t.Fatal(rs)
	}
	check(t, s.Rename(ctx, "admin", a.ID, "A new", "new"))
	check(t, s.Disable(ctx, "admin", a.ID))
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	check(t, s.Enable(ctx, "admin", a.ID))
	newPW, err := s.RegenerateCredentials(ctx, "admin", a.ID, "new-user")
	check(t, err)
	check(t, s.Disable(ctx, "admin", a.ID))
	check(t, s.Delete(ctx, "admin", a.ID, "new"))
	gotBCred, err := os.ReadFile(s.Layout.HtpasswdPath(b.ID))
	check(t, err)
	if !bytes.Equal(bCred, gotBCred) {
		t.Fatal("other site's credentials changed")
	}
	routes, err := s.Links.List()
	check(t, err)
	if len(routes) != 1 || routes["b"] != b.ID {
		t.Fatal(routes)
	}
	audit, err := s.Store.ListAudit(ctx, a.ID)
	check(t, err)
	actions := map[string]bool{}
	for _, e := range audit {
		actions[e.Action] = true
		if strings.Contains(e.Action, pw) || strings.Contains(e.Action, newPW) || strings.Contains(e.Actor, pw) {
			t.Fatal("secret in audit")
		}
	}
	for _, name := range []string{"create", "deploy", "rename", "disable", "enable", "credentials", "delete"} {
		if !actions[name] {
			t.Fatal("missing audit", name)
		}
	}
}

func TestRecoveryReconcilesCredentialUsernameAndCleansOwnedOrphans(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	v, _, err := s.Create(ctx, "admin", "A", "a")
	check(t, err)
	check(t, s.Store.SetAuthUsername(ctx, v.ID, "uncommitted"))
	upload := filepath.Join(s.Layout.StagingDir(), "upload-crash")
	check(t, os.Mkdir(upload, 0700))
	orphanID := "00000000-0000-4000-8000-000000000000"
	check(t, os.Mkdir(s.Layout.SiteDir(orphanID), 0750))
	preserved := filepath.Join(s.Layout.StagingDir(), "operator-note")
	check(t, os.WriteFile(preserved, []byte("keep"), 0600))
	check(t, Recover(ctx, s.Store, s.Layout, s.Links))
	got, err := s.Get(ctx, v.ID)
	check(t, err)
	if got.AuthUsername != "a" {
		t.Fatal(got)
	}
	for _, path := range []string{upload, s.Layout.SiteDir(orphanID)} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("orphan exists", path)
		}
	}
	if _, err = os.Stat(preserved); err != nil {
		t.Fatal(err)
	}
}
func TestRecoveryRejectsReleaseDirectorySymlink(t *testing.T) {
	s := testSites(t)
	ctx := context.Background()
	v, _, err := s.Create(ctx, "admin", "A", "a")
	check(t, err)
	r, err := s.DeployHTML(ctx, "admin", v.ID, strings.NewReader("safe"))
	check(t, err)
	outside := t.TempDir()
	check(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("untouched"), 0600))
	check(t, os.RemoveAll(r.StoragePath))
	check(t, os.Symlink(outside, r.StoragePath))
	if err = Recover(ctx, s.Store, s.Layout, s.Links); err == nil {
		t.Fatal("unsafe release accepted")
	}
	if _, err = os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("external content touched", err)
	}
	routes, err := s.Links.List()
	check(t, err)
	if len(routes) != 0 {
		t.Fatal("unsafe route retained")
	}
}
