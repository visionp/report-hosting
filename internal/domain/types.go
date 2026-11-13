package domain

import (
	"errors"
	"time"
)

type SiteStatus string

const (
	SiteActive   SiteStatus = "active"
	SiteDisabled SiteStatus = "disabled"
)

type ReleaseStatus string

const (
	ReleaseReady      ReleaseStatus = "ready"
	ReleaseActive     ReleaseStatus = "active"
	ReleaseSuperseded ReleaseStatus = "superseded"
)

type Site struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	AuthUsername     string     `json:"auth_username"`
	Status           SiteStatus `json:"status"`
	CurrentReleaseID string     `json:"current_release_id"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type Release struct {
	ID          string        `json:"id"`
	SiteID      string        `json:"site_id"`
	Version     int           `json:"version"`
	StoragePath string        `json:"-"`
	Checksum    string        `json:"checksum"`
	Status      ReleaseStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
}

var (
	ErrInvalidSlug      = errors.New("invalid slug")
	ErrReservedSlug     = errors.New("reserved slug")
	ErrInvalidUsername  = errors.New("invalid username")
	ErrDuplicateSlug    = errors.New("duplicate slug")
	ErrSlugConfirmation = errors.New("slug confirmation mismatch")
	ErrSiteNotDisabled  = errors.New("site must be disabled")
	ErrNotFound         = errors.New("not found")
	ErrNameRequired     = errors.New("site name required")
)
