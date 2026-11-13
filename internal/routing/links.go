// Package routing manages the service-owned Nginx hostname links.
package routing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"report_hosting/internal/domain"
)

type Links struct{ Dir string }

func safeID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func (l Links) Create(slug, siteID string) error {
	if err := domain.ValidateSlug(slug, nil); err != nil {
		return err
	}
	if !safeID(siteID) {
		return errors.New("invalid storage ID")
	}
	if err := os.MkdirAll(l.Dir, 0750); err != nil {
		return err
	}
	// Creating a symlink is atomic and never overwrites another site's route.
	err := os.Symlink(filepath.Join("..", "sites", siteID), filepath.Join(l.Dir, slug))
	if os.IsExist(err) {
		return domain.ErrDuplicateSlug
	}
	return err
}
func (l Links) Remove(slug string) error {
	if err := domain.ValidateSlug(slug, nil); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(l.Dir, slug))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (l Links) List() (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(l.Dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err = domain.ValidateSlug(e.Name(), nil); err != nil {
			return nil, errors.New("unexpected routing entry")
		}
		target, err := os.Readlink(filepath.Join(l.Dir, e.Name()))
		if err != nil {
			return nil, err
		}
		id := filepath.Base(target)
		if !safeID(id) || filepath.Clean(target) != filepath.Join("..", "sites", id) {
			return nil, errors.New("unexpected routing target")
		}
		out[e.Name()] = id
	}
	return out, nil
}

// ErrStaleLink reports that a rename committed but the old link could not be removed.
var ErrStaleLink = errors.New("old routing link not removed")

func (l Links) Rename(oldSlug, newSlug, siteID string, commit func() error) error {
	if oldSlug == newSlug {
		return commit()
	}
	if err := l.Create(newSlug, siteID); err != nil {
		return err
	}
	if err := commit(); err != nil {
		_ = l.Remove(newSlug)
		return err
	}
	if err := l.Remove(oldSlug); err != nil {
		return fmt.Errorf("%w: %v", ErrStaleLink, err)
	}
	return nil
}
