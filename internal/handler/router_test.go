package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"report_hosting/internal/config"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/service"
	"report_hosting/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestUploadCredentialsAndDelete(t *testing.T) {
	h, id := testRouter(t)
	origin := "https://admin.partnersflex.space"
	w := request(h, "POST", "/api/sites", `{"name":"upload","slug":"upload"}`, id, origin, csrf(id))
	var created struct {
		Site     struct{ ID string }
		Password string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Site.ID == "" {
		t.Fatal(w.Body.String())
	}
	base := "/api/sites/" + created.Site.ID
	for _, tc := range []struct {
		content string
		status  int
	}{{"<html>published</html>", 200}, {"", 400}} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", "report.html")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(tc.content))
		mw.Close()
		r := httptest.NewRequest("POST", origin+base+"/deploy", &buf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf(id))
		r.AddCookie(SessionCookie(id))
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("upload: %d %s", w.Code, w.Body.String())
		}
	}
	w = request(h, "POST", base+"/credentials", `{"username":"new-user"}`, id, origin, csrf(id))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/sites/"+created.Site.ID, "", id, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "new-user") || strings.Contains(w.Body.String(), "src=\"https://") {
		t.Fatal("invalid detail page")
	}
	if w = request(h, "POST", base+"/disable", "", id, origin, csrf(id)); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "DELETE", base, `{"slug":"wrong"}`, id, origin, csrf(id)); w.Code != 400 {
		t.Fatal("missing confirmation enforcement")
	}
	if w = request(h, "DELETE", base, `{"slug":"upload"}`, id, origin, csrf(id)); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "GET", base, "", id, "", ""); w.Code != 404 {
		t.Fatal("deleted site remains")
	}
}

func testRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	c := config.Defaults()
	c.Production = false
	c.AccessCheck = false
	c.DataDir = t.TempDir()
	layout := storage.Layout{Root: c.DataDir}
	if err := layout.Init(); err != nil {
		t.Fatal(err)
	}
	st, err := repository.Open(filepath.Join(c.DataDir, "db", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := service.HashPassword("password", config.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateAdmin(context.Background(), "admin", hash); err != nil {
		t.Fatal(err)
	}
	auth := &service.Auth{Store: st}
	id, err := auth.Login(context.Background(), "admin", "password", "ip")
	if err != nil {
		t.Fatal(err)
	}
	sites := service.NewSites(st, layout, routing.Links{Dir: layout.BySlugDir()}, storage.NewDeployer(layout, st, c.Limits, 0), c.ReservedSlugs)
	return NewRouter(Deps{Config: c, Auth: auth, Sites: sites}), id
}
func request(h http.Handler, method, path, body, id, origin, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://admin.partnersflex.space"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if id != "" {
		r.AddCookie(SessionCookie(id))
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if token != "" {
		r.Header.Set("X-CSRF-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAPISessionAndCSRF(t *testing.T) {
	h, id := testRouter(t)
	for _, path := range []string{"/api/sites", "/api/sites/x", "/api/sites/x/releases"} {
		if w := request(h, "GET", path, "", "", "", ""); w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, tc := range []struct{ origin, token string }{{"https://admin.partnersflex.space", ""}, {"https://evil.partnersflex.space", csrf(id)}} {
		if w := request(h, "POST", "/api/sites", `{"name":"test","slug":"test"}`, id, tc.origin, tc.token); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	w := request(h, "POST", "/api/sites", `{"name":"<script>","slug":"test"}`, id, "https://admin.partnersflex.space", csrf(id))
	if w.Code != 201 || !strings.Contains(w.Body.String(), "password") {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/sites", "", id, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("list: %s", w.Body.String())
	}
	w = request(h, "GET", "/", "", id, "", "")
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("template did not escape name")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("missing security headers")
	}
}
func TestAccessDefaultFailClosed(t *testing.T) {
	h := NewRouter(Deps{Config: config.Defaults()})
	w := request(h, "GET", "/login", "", "", "", "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestCookie(t *testing.T) {
	c := SessionCookie("token")
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || !strings.HasPrefix(c.Name, "__Host-") {
		t.Fatal(c)
	}
}

type testKeys struct{ k *rsa.PublicKey }

func (k testKeys) Key(_ context.Context, id string) (*rsa.PublicKey, error) {
	if id != "test" {
		return nil, errors.New("unknown key")
	}
	return k.k, nil
}
func TestAccessVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v := NewAccessVerifier("team.cloudflareaccess.com", "aud", testKeys{&key.PublicKey})
	for _, tc := range []struct {
		issuer, aud string
		expiry      time.Time
		valid       bool
	}{{"https://team.cloudflareaccess.com", "aud", time.Now().Add(time.Hour), true}, {"wrong", "aud", time.Now().Add(time.Hour), false}, {"https://team.cloudflareaccess.com", "wrong", time.Now().Add(time.Hour), false}, {"https://team.cloudflareaccess.com", "aud", time.Now().Add(-time.Hour), false}} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{Issuer: tc.issuer, Audience: jwt.ClaimStrings{tc.aud}, ExpiresAt: jwt.NewNumericDate(tc.expiry)})
		token.Header["kid"] = "test"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		if (v.Verify(context.Background(), raw) == nil) != tc.valid {
			t.Fatal("JWT claim validation failed")
		}
	}
	if v.Verify(context.Background(), "") == nil || v.Verify(context.Background(), "invalid") == nil {
		t.Fatal("invalid token accepted")
	}
}
