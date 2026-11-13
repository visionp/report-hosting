package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/storage"
	"strings"
)

// Recover runs before the HTTP listener; current decides publication, SQLite decides routes.
func Recover(ctx context.Context, st *repository.Store, layout storage.Layout, links routing.Links) error {
	if err := layout.Init(); err != nil {
		return err
	}
	sites, err := st.ListSites(ctx)
	if err != nil {
		return err
	}
	staging, err := os.ReadDir(layout.StagingDir())
	if err != nil {
		return err
	}
	for _, e := range staging {
		if strings.HasPrefix(e.Name(), "upload-") || strings.HasPrefix(e.Name(), "incoming-") {
			if err = os.RemoveAll(filepath.Join(layout.StagingDir(), e.Name())); err != nil {
				return err
			}
		}
	}
	wanted := map[string]string{}
	owned := map[string]bool{}
	for _, site := range sites {
		// Validate database IDs before constructing paths used by recovery cleanup.
		if filepath.Base(site.ID) != site.ID || site.ID == "." || site.ID == ".." || site.ID == "" {
			return errors.New("invalid site storage ID")
		}
		owned[site.ID] = true
		for _, dir := range []string{layout.SiteDir(site.ID), filepath.Join(layout.SiteDir(site.ID), "releases")} {
			info, e := os.Lstat(dir)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				_ = links.Remove(site.Slug)
				return errors.New("unexpected storage link")
			}
		}
		credential, err := os.ReadFile(layout.HtpasswdPath(site.ID))
		if err != nil {
			return err
		}
		line := strings.TrimSuffix(string(credential), "\n")
		parts := strings.Split(line, ":")
		if len(parts) != 2 || strings.ContainsAny(line, "\r\n") || domain.ValidateUsername(parts[0]) != nil || !strings.HasPrefix(parts[1], "{SSHA}") {
			return errors.New("invalid credential file")
		}
		decoded, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(parts[1], "{SSHA}"))
		if e != nil || len(decoded) < 28 {
			return errors.New("invalid credential hash")
		}
		if parts[0] != site.AuthUsername {
			if err = st.SetAuthUsername(ctx, site.ID, parts[0]); err != nil {
				return err
			}
		}
		releases, err := st.ListReleases(ctx, site.ID)
		if err != nil {
			return err
		}
		currentPath := filepath.Join(layout.SiteDir(site.ID), "current")
		target, err := os.Readlink(currentPath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		currentID := ""
		if err == nil {
			for _, r := range releases {
				if target == filepath.Join("releases", fmt.Sprintf("%06d", r.Version)) {
					info, e := os.Lstat(filepath.Join(layout.SiteDir(site.ID), target))
					if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
						_ = links.Remove(site.Slug)
						return errors.New("current release missing")
					}
					currentID = r.ID
					break
				}
			}
			if currentID == "" {
				_ = links.Remove(site.Slug)
				return errors.New("current points to unknown release")
			}
			if err = st.ActivateRelease(ctx, site.ID, currentID); err != nil {
				return err
			}
		} else if site.CurrentReleaseID != "" {
			_ = links.Remove(site.Slug)
			return errors.New("current release link missing")
		}
		for _, r := range releases {
			if r.Status == domain.ReleaseReady && r.ID != currentID {
				expected := layout.ReleaseDir(site.ID, r.Version)
				if filepath.Clean(r.StoragePath) != filepath.Clean(expected) {
					return errors.New("unexpected release storage path")
				}
				if err = os.RemoveAll(expected); err != nil {
					return err
				}
				if err = st.DeleteRelease(ctx, r.ID); err != nil {
					return err
				}
			}
		}
		// Files moved before AddRelease are not represented in metadata. Only remove
		// numeric release directories owned by this known site, never arbitrary paths.
		known := map[string]bool{}
		for _, r := range releases {
			known[fmt.Sprintf("%06d", r.Version)] = true
		}
		entries, e := os.ReadDir(filepath.Join(layout.SiteDir(site.ID), "releases"))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		for _, entry := range entries {
			var n int
			if _, e = fmt.Sscanf(entry.Name(), "%d", &n); e == nil && n > 0 && entry.Name() == fmt.Sprintf("%06d", n) && entry.IsDir() && !known[entry.Name()] {
				if e = os.RemoveAll(layout.ReleaseDir(site.ID, n)); e != nil {
					return e
				}
			}
		}
		if site.Status == domain.SiteActive {
			wanted[site.Slug] = site.ID
		}
	}
	siteDirs, err := os.ReadDir(filepath.Join(layout.Root, "sites"))
	if err != nil {
		return err
	}
	uuid := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	for _, entry := range siteDirs {
		if uuid.MatchString(entry.Name()) && !owned[entry.Name()] {
			if err = os.RemoveAll(layout.SiteDir(entry.Name())); err != nil {
				return err
			}
		}
	}
	existing, err := links.List()
	if err != nil {
		return err
	}
	for slug, id := range existing {
		if wanted[slug] != id {
			if err = links.Remove(slug); err != nil {
				return err
			}
			delete(existing, slug)
		}
	}
	for slug, id := range wanted {
		if existing[slug] != id {
			if err = links.Create(slug, id); err != nil {
				return err
			}
		}
	}
	return nil
}
