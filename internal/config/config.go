package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Limits struct {
	MaxZipBytes       int64 `json:"max_zip_bytes"`
	MaxExtractedBytes int64 `json:"max_extracted_bytes"`
	MaxFiles          int   `json:"max_files"`
	MaxRatio          int   `json:"max_ratio"`
}
type Argon2Params struct {
	Memory      uint32 `json:"memory"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
}
type Config struct {
	DataDir          string       `json:"data_dir"`
	ListenAddr       string       `json:"listen_addr"`
	BaseDomain       string       `json:"base_domain"`
	Limits           Limits       `json:"limits"`
	ReservedSlugs    []string     `json:"reserved_slugs"`
	Production       bool         `json:"production"`
	AccessCheck      bool         `json:"access_check"`
	AccessTeamDomain string       `json:"access_team_domain"`
	AccessAUD        string       `json:"access_aud"`
	Argon2           Argon2Params `json:"argon2"`
}

func Defaults() Config {
	return Config{DataDir: "/var/lib/report_hosting", ListenAddr: "127.0.0.1:8080", BaseDomain: "partnersflex.space",
		Limits: Limits{50 << 20, 250 << 20, 5000, 100}, ReservedSlugs: []string{"admin", "www", "api", "mail", "ftp", "status", "cdn", "static", "ns1", "ns2"},
		Production: true, AccessCheck: true, Argon2: Argon2Params{64 * 1024, 3, 2}}
}

// Load applies defaults, then a strict JSON file, then environment overrides.
// Explicit local development configuration is required to disable Access.
func Load(path string) (Config, error) {
	c := Defaults()
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 1<<20))
		d.DisallowUnknownFields()
		if err = d.Decode(&c); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		var extra any
		if err = d.Decode(&extra); err != io.EOF {
			return Config{}, errors.New("config must contain one JSON object")
		}
	}
	for key, dest := range map[string]*string{"DATA_DIR": &c.DataDir, "LISTEN_ADDR": &c.ListenAddr, "BASE_DOMAIN": &c.BaseDomain, "ACCESS_TEAM_DOMAIN": &c.AccessTeamDomain, "ACCESS_AUD": &c.AccessAUD} {
		if v, ok := os.LookupEnv("REPORT_HOSTING_" + key); ok {
			*dest = v
		}
	}
	for key, dest := range map[string]*bool{"PRODUCTION": &c.Production, "ACCESS_CHECK": &c.AccessCheck} {
		if v, ok := os.LookupEnv("REPORT_HOSTING_" + key); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return Config{}, fmt.Errorf("invalid REPORT_HOSTING_%s boolean", key)
			}
			*dest = b
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("data_dir is required")
	}
	if c.Production && !filepath.IsAbs(c.DataDir) {
		return errors.New("production data_dir must be absolute")
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return errors.New("listen_addr must be host:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listen_addr must use a loopback IP")
	}
	if c.BaseDomain != "partnersflex.space" {
		return errors.New("base_domain must be partnersflex.space")
	}
	if c.Production && !c.AccessCheck {
		return errors.New("production requires Access verification")
	}
	if c.AccessCheck {
		if c.AccessAUD == "" || !strings.HasSuffix(c.AccessTeamDomain, ".cloudflareaccess.com") || strings.ContainsAny(c.AccessTeamDomain, "/: ?#\\") {
			return errors.New("Access verification requires access_aud and a cloudflareaccess.com team hostname")
		}
	}
	if c.Limits.MaxZipBytes <= 0 || c.Limits.MaxExtractedBytes <= 0 || c.Limits.MaxFiles <= 0 || c.Limits.MaxRatio <= 0 {
		return errors.New("all upload limits must be positive")
	}
	if c.Argon2.Memory < 8*uint32(c.Argon2.Parallelism) || c.Argon2.Parallelism == 0 || c.Argon2.Iterations == 0 {
		return errors.New("invalid Argon2 parameters")
	}
	if c.Production && (c.Argon2.Memory < 64*1024 || c.Argon2.Iterations < 3 || c.Argon2.Parallelism < 2) {
		return errors.New("production Argon2 parameters below required minimum")
	}
	return nil
}
