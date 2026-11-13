package config

import (
	"os"
	"path/filepath"
	"testing"
)

func file(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Limits != (Limits{50 << 20, 250 << 20, 5000, 100}) || !c.AccessCheck || !c.Production || len(c.ReservedSlugs) != 10 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}
func TestLocalConfig(t *testing.T) {
	c, err := Load(file(t, `{"production":false,"access_check":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessCheck || c.Limits.MaxFiles != 5000 {
		t.Fatal("defaults not merged")
	}
}
func TestRejectUnsafeConfig(t *testing.T) {
	for _, s := range []string{`{"production":true,"access_check":false}`, `{"production":false,"access_check":false,"listen_addr":"0.0.0.0:8080"}`, `{"production":false,"access_check":false,"limits":{"max_files":0}}`, `{"unknown":true}`, `{} {}`, `null`} {
		t.Run(s, func(t *testing.T) {
			if _, err := Load(file(t, s)); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
func TestEnvPrecedence(t *testing.T) {
	t.Setenv("REPORT_HOSTING_ACCESS_AUD", "test-audience")
	t.Setenv("REPORT_HOSTING_ACCESS_TEAM_DOMAIN", "team.cloudflareaccess.com")
	c, err := Load(file(t, `{"production":false,"access_aud":"file"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessAUD != "test-audience" {
		t.Fatal("environment ignored")
	}
}
