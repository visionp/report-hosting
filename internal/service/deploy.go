package service

import (
	"context"
	"errors"
	"io"

	"report_hosting/internal/domain"
	"report_hosting/internal/storage"
)

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
