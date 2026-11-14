// Package handler is the HTTP adapter. It checks the host, access, session,
// and CSRF, then calls the service.
package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"

	"report_hosting/internal/config"
	"report_hosting/internal/service"
	"report_hosting/web"
)

const cookieName = "__Host-report_hosting_session"

type Deps struct {
	Config config.Config
	Sites  *service.Sites
	Auth   *service.Auth
	Access AccessVerifier
}
type router struct{ Deps }

func SessionCookie(id string) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: id, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/"}
}
func csrf(id string) string {
	s := sha256.Sum256([]byte("report-hosting-csrf:" + id))
	return hex.EncodeToString(s[:])
}
func NewRouter(d Deps) http.Handler {
	h := &router{d}
	return http.HandlerFunc(h.serve)
}
func (h *router) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	host := "admin." + h.Config.BaseDomain
	if r.Host != host {
		http.Error(w, "invalid host", 403)
		return
	}
	if h.Config.AccessCheck || h.Config.Production {
		if h.Access == nil || h.Access.Verify(r.Context(), r.Header.Get("Cf-Access-Jwt-Assertion")) != nil {
			http.Error(w, "access denied", 403)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.Config.Limits.MaxZipBytes+(1<<20))
	mutation := r.Method != "GET" && r.Method != "HEAD"
	if mutation && r.Header.Get("Origin") != "https://"+host {
		http.Error(w, "invalid origin", 403)
		return
	}
	if r.URL.Path == "/login" {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		h.login(w, r)
		return
	}
	staticTypes := map[string]string{
		"/static/htmx.min.js":             "text/javascript; charset=utf-8",
		"/static/copy-password.js":        "text/javascript; charset=utf-8",
		"/static/styles.css":              "text/css; charset=utf-8",
		"/static/flex-partners-white.svg": "image/svg+xml",
	}
	if contentType, ok := staticTypes[r.URL.Path]; ok && r.Method == "GET" {
		data, err := web.Assets.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
		return
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		h.unauthorized(w, r)
		return
	}
	if _, err = h.Auth.Validate(r.Context(), c.Value); err != nil {
		h.unauthorized(w, r)
		return
	}
	if mutation {
		token := r.Header.Get("X-CSRF-Token")
		if token == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			if err = r.ParseForm(); err != nil {
				h.fail(w, err)
				return
			}
			token = r.Form.Get("csrf")
		}
		if token == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err = r.ParseMultipartForm(1 << 20); err != nil {
				h.fail(w, err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			token = r.FormValue("csrf")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(csrf(c.Value))) != 1 {
			http.Error(w, "invalid CSRF token", 403)
			return
		}
	}
	if r.URL.Path == "/logout" && r.Method == "POST" {
		if err = h.Auth.Logout(r.Context(), c.Value); err != nil {
			h.fail(w, err)
			return
		}
		expired := SessionCookie("")
		expired.MaxAge = -1
		http.SetCookie(w, expired)
		http.Redirect(w, r, "/login", 303)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.api(w, r)
		return
	}
	h.ui(w, r, csrf(c.Value))
}
func (h *router) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
	} else {
		http.Redirect(w, r, "/login", 303)
	}
}

func (h *router) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		render(w, "login", nil)
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, err)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	// Nginx supplies the validated visitor address, and only loopback may proxy it.
	if remote := net.ParseIP(ip); remote != nil && remote.IsLoopback() {
		if forwarded := net.ParseIP(r.Header.Get("X-Real-IP")); forwarded != nil {
			ip = forwarded.String()
		}
	}
	id, err := h.Auth.Login(r.Context(), r.Form.Get("username"), r.Form.Get("password"), ip)
	if err != nil {
		if errors.Is(err, service.ErrLocked) {
			renderStatus(w, "login", struct{ Error string }{"Too many attempts. Please try again in 15 minutes."}, http.StatusTooManyRequests)
		} else {
			renderStatus(w, "login", struct{ Error string }{"The username or password is incorrect. Please try again."}, http.StatusUnauthorized)
		}
		return
	}
	http.SetCookie(w, SessionCookie(id))
	http.Redirect(w, r, "/", 303)
}
