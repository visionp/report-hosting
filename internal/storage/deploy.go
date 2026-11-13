package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"report_hosting/internal/domain"
)

var ErrInsufficientDisk = errors.New("insufficient disk space")

type ReleaseStore interface {
	GetSite(context.Context, string) (domain.Site, error)
	NextVersion(context.Context, string) (int, error)
	AddRelease(context.Context, domain.Release) error
	ActivateRelease(context.Context, string, string) error
	ListReleases(context.Context, string) ([]domain.Release, error)
	DeleteRelease(context.Context, string) error
}

type Deployer struct {
	Layout       Layout
	Store        ReleaseStore
	Limits       Limits
	MinFreeBytes int64
	mu           sync.Mutex
	locks        map[string]*sync.Mutex
	afterSwitch  func() error
	freeSpace    func(string) (uint64, error)
}

func NewDeployer(layout Layout, store ReleaseStore, limits Limits, minFreeBytes int64) *Deployer {
	return &Deployer{Layout: layout, Store: store, Limits: limits, MinFreeBytes: minFreeBytes}
}

func (d *Deployer) siteLock(id string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locks == nil {
		d.locks = make(map[string]*sync.Mutex)
	}
	if d.locks[id] == nil {
		d.locks[id] = new(sync.Mutex)
	}
	return d.locks[id]
}

func (d *Deployer) DeployZip(ctx context.Context, siteID, zipPath string) (domain.Release, error) {
	return d.deploy(ctx, siteID, func(dir string) (string, error) { return ExtractZip(zipPath, dir, d.Limits) })
}

func (d *Deployer) DeployHTML(ctx context.Context, siteID string, r io.Reader) (domain.Release, error) {
	return d.deploy(ctx, siteID, func(dir string) (string, error) { return StageHTML(r, dir, d.Limits.MaxZipBytes) })
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (d *Deployer) deploy(ctx context.Context, siteID string, stage func(string) (string, error)) (domain.Release, error) {
	lock := d.siteLock(siteID)
	lock.Lock()
	defer lock.Unlock()
	var empty domain.Release
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if _, err := d.Store.GetSite(ctx, siteID); err != nil {
		return empty, err
	}
	if err := d.Layout.Init(); err != nil {
		return empty, err
	}
	space := d.freeSpace
	if space == nil {
		space = availableBytes
	}
	free, err := space(d.Layout.Root)
	if err != nil {
		return empty, err
	}
	if d.MinFreeBytes < 0 || d.Limits.MaxExtractedBytes <= 0 || free < uint64(d.MinFreeBytes) || free-uint64(d.MinFreeBytes) < uint64(d.Limits.MaxExtractedBytes) {
		return empty, ErrInsufficientDisk
	}
	dir, err := os.MkdirTemp(d.Layout.StagingDir(), "upload-*")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(dir)
	root, err := stage(dir)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	checksum, err := treeChecksum(root)
	if err != nil {
		return empty, err
	}
	version, err := d.Store.NextVersion(ctx, siteID)
	if err != nil {
		return empty, err
	}
	id, err := randomID()
	if err != nil {
		return empty, err
	}
	dest := d.Layout.ReleaseDir(siteID, version)
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return empty, err
	}
	if _, err := os.Lstat(dest); err == nil {
		return empty, errors.New("release directory already exists")
	} else if !os.IsNotExist(err) {
		return empty, err
	}
	if err := os.Rename(root, dest); err != nil {
		return empty, err
	}
	// MkdirTemp starts at 0700. Published roots must be readable by Nginx's group.
	if err := os.Chmod(dest, 0750); err != nil {
		os.RemoveAll(dest)
		return empty, err
	}
	release := domain.Release{ID: id, SiteID: siteID, Version: version, StoragePath: dest, Checksum: checksum, Status: domain.ReleaseReady, CreatedAt: time.Now().UTC()}
	if err := d.Store.AddRelease(ctx, release); err != nil {
		os.RemoveAll(dest)
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		d.Store.DeleteRelease(context.WithoutCancel(ctx), id)
		os.RemoveAll(dest)
		return empty, err
	}
	siteDir := d.Layout.SiteDir(siteID)
	tmp := filepath.Join(siteDir, ".current-"+id)
	if err := os.Symlink(filepath.Join("releases", fmt.Sprintf("%06d", version)), tmp); err != nil {
		d.Store.DeleteRelease(context.WithoutCancel(ctx), id)
		os.RemoveAll(dest)
		return empty, err
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, filepath.Join(siteDir, "current")); err != nil {
		d.Store.DeleteRelease(context.WithoutCancel(ctx), id)
		os.RemoveAll(dest)
		return empty, err
	}
	// Once current changes, keep this ready release even if the database write
	// fails. Startup recovery treats current as the authoritative published state.
	if d.afterSwitch != nil {
		if err := d.afterSwitch(); err != nil {
			return release, err
		}
	}
	if err := d.Store.ActivateRelease(context.WithoutCancel(ctx), siteID, id); err != nil {
		return release, err
	}
	release.Status = domain.ReleaseActive
	// The release is live; pruning is best-effort and must not fail the deploy.
	if err := d.prune(context.WithoutCancel(ctx), siteID); err != nil {
		log.Printf("prune releases for site %s: %v", siteID, err)
	}
	return release, nil
}

func (d *Deployer) prune(ctx context.Context, siteID string) error {
	releases, err := d.Store.ListReleases(ctx, siteID)
	if err != nil {
		return err
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Version > releases[j].Version })
	previous := 0
	for _, r := range releases {
		if r.Status != domain.ReleaseSuperseded {
			continue
		}
		previous++
		if previous <= 2 {
			continue
		}
		// Derive the deletion path from controlled IDs, never a database path.
		if err := os.RemoveAll(d.Layout.ReleaseDir(siteID, r.Version)); err != nil {
			return err
		}
		if err := d.Store.DeleteRelease(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func treeChecksum(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !e.Type().IsRegular() {
			return ErrSpecialFile
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(filepath.ToSlash(rel)), filepath.ToSlash(rel), info.Size())
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
