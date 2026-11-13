package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout describes service-owned storage. IDs and versions come from the service.
type Layout struct{ Root string }

func (l Layout) SiteDir(id string) string { return filepath.Join(l.Root, "sites", id) }
func (l Layout) ReleaseDir(id string, version int) string {
	return filepath.Join(l.SiteDir(id), "releases", fmt.Sprintf("%06d", version))
}
func (l Layout) StagingDir() string            { return filepath.Join(l.Root, "staging") }
func (l Layout) BySlugDir() string             { return filepath.Join(l.Root, "by-slug") }
func (l Layout) HtpasswdPath(id string) string { return filepath.Join(l.SiteDir(id), ".htpasswd") }

func (l Layout) Init() error {
	for _, dir := range []string{l.Root, filepath.Join(l.Root, "sites"), l.BySlugDir()} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	for _, dir := range []string{filepath.Join(l.Root, "db"), l.StagingDir()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}
