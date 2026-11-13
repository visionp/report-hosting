package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/storage"
	"strings"
	"sync"
	"time"
)

type Sites struct {
	Store    *repository.Store
	Layout   storage.Layout
	Links    routing.Links
	Deployer *storage.Deployer
	Reserved []string
	Logger   *slog.Logger
	mu       sync.Mutex
}

func NewSites(st *repository.Store, layout storage.Layout, links routing.Links, deployer *storage.Deployer, reserved []string) *Sites {
	return &Sites{Store: st, Layout: layout, Links: links, Deployer: deployer, Reserved: reserved}
}
func (s *Sites) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// record emits only fixed action codes and service-owned IDs, never content or secrets.
func (s *Sites) record(ctx context.Context, actor, action, id string) error {
	err := s.Store.AddAudit(ctx, actor, action, id)
	if err != nil {
		s.logger().ErrorContext(ctx, "site audit failed", "action", action, "site_id", id)
		return err
	}
	s.logger().InfoContext(ctx, "site action recorded", "action", action, "site_id", id)
	return nil
}
func (s *Sites) deployResult(ctx context.Context, id string, err error) {
	if err == nil {
		return
	}
	action := "deploy"
	if errors.Is(err, storage.ErrUnsafePath) || errors.Is(err, storage.ErrSpecialFile) || errors.Is(err, storage.ErrDuplicatePath) || errors.Is(err, storage.ErrLimitExceeded) || errors.Is(err, storage.ErrNoIndex) || errors.Is(err, storage.ErrBadLayout) || errors.Is(err, storage.ErrEmptyUpload) {
		action = "validation"
	}
	s.logger().WarnContext(ctx, "site deployment rejected", "action", action, "site_id", id)
}
func (s *Sites) Get(ctx context.Context, id string) (domain.Site, error) {
	return s.Store.GetSite(ctx, id)
}
func (s *Sites) List(ctx context.Context) ([]domain.Site, error) { return s.Store.ListSites(ctx) }
func (s *Sites) Releases(ctx context.Context, id string) ([]domain.Release, error) {
	if _, err := s.Store.GetSite(ctx, id); err != nil {
		return nil, err
	}
	return s.Store.ListReleases(ctx, id)
}
func newSiteID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (s *Sites) Create(ctx context.Context, actor, name, slug string) (domain.Site, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var empty domain.Site
	if err := domain.ValidateSlug(slug, s.Reserved); err != nil {
		return empty, "", err
	}
	if strings.TrimSpace(name) == "" {
		return empty, "", domain.ErrNameRequired
	}
	if _, err := s.Store.GetSiteBySlug(ctx, slug); err == nil {
		return empty, "", domain.ErrDuplicateSlug
	} else if !errors.Is(err, domain.ErrNotFound) {
		return empty, "", err
	}
	id, err := newSiteID()
	if err != nil {
		return empty, "", err
	}
	pw, err := storage.NewPassword()
	if err != nil {
		return empty, "", err
	}
	if err = s.Layout.Init(); err != nil {
		return empty, "", err
	}
	dir := s.Layout.SiteDir(id)
	if err = os.Mkdir(dir, 0750); err != nil {
		return empty, "", err
	}
	if err = storage.WriteHtpasswd(dir, slug, pw); err != nil {
		os.RemoveAll(dir)
		return empty, "", err
	}
	now := time.Now().UTC()
	v := domain.Site{ID: id, Name: name, Slug: slug, AuthUsername: slug, Status: domain.SiteActive, CreatedAt: now, UpdatedAt: now}
	if err = s.Store.CreateSite(ctx, v); err != nil {
		os.RemoveAll(dir)
		return empty, "", err
	}
	if err = s.Links.Create(slug, id); err != nil {
		_ = s.Store.DeleteSite(context.WithoutCancel(ctx), id)
		os.RemoveAll(dir)
		return empty, "", err
	}
	if err = s.record(context.WithoutCancel(ctx), actor, "create", id); err != nil {
		return v, pw, err
	}
	return v, pw, nil
}
func (s *Sites) Rename(ctx context.Context, actor, id, name, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := domain.ValidateSlug(slug, s.Reserved); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return domain.ErrNameRequired
	}
	v, err := s.Store.GetSite(ctx, id)
	if err != nil {
		return err
	}
	commit := func() error { return s.Store.UpdateSite(ctx, id, name, slug) }
	if v.Status == domain.SiteDisabled {
		err = commit()
	} else {
		err = s.Links.Rename(v.Slug, slug, id, commit)
	}
	// The rename is committed; a leftover old link is repaired by startup recovery.
	if errors.Is(err, routing.ErrStaleLink) {
		s.logger().ErrorContext(ctx, "old routing link not removed", "action", "rename", "site_id", id)
		err = nil
	}
	if err != nil {
		return err
	}
	return s.record(context.WithoutCancel(ctx), actor, "rename", id)
}
func (s *Sites) Disable(ctx context.Context, actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetSite(ctx, id)
	if err != nil {
		return err
	}
	// Database first: recovery must never reopen a committed disable.
	if err = s.Store.SetSiteStatus(ctx, id, domain.SiteDisabled); err != nil {
		return err
	}
	if err = s.Links.Remove(v.Slug); err != nil {
		return err
	}
	return s.record(context.WithoutCancel(ctx), actor, "disable", id)
}
func (s *Sites) Enable(ctx context.Context, actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetSite(ctx, id)
	if err != nil {
		return err
	}
	if v.Status == domain.SiteActive {
		return nil
	}
	if err = s.Links.Create(v.Slug, id); err != nil {
		return err
	}
	if err = s.Store.SetSiteStatus(ctx, id, domain.SiteActive); err != nil {
		_ = s.Links.Remove(v.Slug)
		return err
	}
	return s.record(context.WithoutCancel(ctx), actor, "enable", id)
}
func (s *Sites) Delete(ctx context.Context, actor, id, confirmSlug string) error {
	// Lock order: per-site deploy lock, then s.mu. Deploys take only the site
	// lock, so a deploy in flight finishes before the directory is removed.
	unlockSite := s.Deployer.LockSite(id)
	defer unlockSite()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetSite(ctx, id)
	if err != nil {
		return err
	}
	if v.Status != domain.SiteDisabled {
		return domain.ErrSiteNotDisabled
	}
	if v.Slug != confirmSlug {
		return domain.ErrSlugConfirmation
	}
	if err = s.Links.Remove(v.Slug); err != nil {
		return err
	}
	// Audit only after the database delete succeeded, so the log never claims a
	// deletion that did not happen. The audit table has no FK to sites.
	if err = s.Store.DeleteSite(ctx, id); err != nil {
		return err
	}
	rmErr := os.RemoveAll(s.Layout.SiteDir(id))
	if err = s.record(context.WithoutCancel(ctx), actor, "delete", id); err != nil {
		return err
	}
	return rmErr
}
func (s *Sites) RegenerateCredentials(ctx context.Context, actor, id, username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := domain.ValidateUsername(username); err != nil {
		return "", err
	}
	v, err := s.Store.GetSite(ctx, id)
	if err != nil {
		return "", err
	}
	pw, err := storage.NewPassword()
	if err != nil {
		return "", err
	}
	// Update metadata first; a failed file write rolls it back. Authentication reads the file.
	if err = s.Store.SetAuthUsername(ctx, id, username); err != nil {
		return "", err
	}
	if err = storage.WriteHtpasswd(s.Layout.SiteDir(id), username, pw); err != nil {
		_ = s.Store.SetAuthUsername(context.WithoutCancel(ctx), id, v.AuthUsername)
		return "", err
	}
	return pw, s.record(context.WithoutCancel(ctx), actor, "credentials", id)
}
func (s *Sites) DeployZip(ctx context.Context, actor, id, path string) (domain.Release, error) {
	// No s.mu here: Deployer serializes per site, so a large upload does not block other sites.
	if _, err := s.Store.GetSite(ctx, id); err != nil {
		return domain.Release{}, err
	}
	s.logger().InfoContext(ctx, "site upload received", "action", "upload", "site_id", id)
	v, err := s.Deployer.DeployZip(ctx, id, path)
	if err == nil {
		err = s.record(context.WithoutCancel(ctx), actor, "deploy", id)
	}
	s.deployResult(ctx, id, err)
	return v, err
}
func (s *Sites) DeployHTML(ctx context.Context, actor, id string, r io.Reader) (domain.Release, error) {
	// No s.mu here: Deployer serializes per site, so a large upload does not block other sites.
	if _, err := s.Store.GetSite(ctx, id); err != nil {
		return domain.Release{}, err
	}
	s.logger().InfoContext(ctx, "site upload received", "action", "upload", "site_id", id)
	v, err := s.Deployer.DeployHTML(ctx, id, r)
	if err == nil {
		err = s.record(context.WithoutCancel(ctx), actor, "deploy", id)
	}
	s.deployResult(ctx, id, err)
	return v, err
}
