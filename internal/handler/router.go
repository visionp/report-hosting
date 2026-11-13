package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"report_hosting/internal/config"
	"report_hosting/internal/domain"
	"report_hosting/internal/service"
	"report_hosting/internal/storage"
	"report_hosting/web"
	"strings"
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
	if r.URL.Path == "/static/htmx.min.js" && r.Method == "GET" {
		data, err := web.Assets.ReadFile("static/htmx.min.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
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
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}
func (h *router) fail(w http.ResponseWriter, err error) {
	status, code := 500, "internal_error"
	var large *http.MaxBytesError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status, code = 404, "not_found"
	case errors.As(err, &large), errors.Is(err, storage.ErrLimitExceeded):
		status, code = 413, "upload_too_large"
	case errors.Is(err, domain.ErrDuplicateSlug):
		status, code = 409, "duplicate_slug"
	case errors.Is(err, domain.ErrInvalidSlug), errors.Is(err, domain.ErrReservedSlug), errors.Is(err, domain.ErrInvalidUsername), errors.Is(err, domain.ErrSlugConfirmation), errors.Is(err, domain.ErrSiteNotDisabled), errors.Is(err, storage.ErrUnsafePath), errors.Is(err, storage.ErrSpecialFile), errors.Is(err, storage.ErrDuplicatePath), errors.Is(err, storage.ErrNoIndex), errors.Is(err, storage.ErrBadLayout), errors.Is(err, storage.ErrEmptyUpload):
		status, code = 400, "invalid_input"
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func (h *router) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		render(w, `<h1>Report hosting</h1><form method="post"><label>Username <input name="username" required></label><label>Password <input type="password" name="password" required></label><button>Sign in</button></form>`, nil)
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
			http.Error(w, "temporarily locked", 429)
		} else {
			http.Error(w, "login failed", 401)
		}
		return
	}
	http.SetCookie(w, SessionCookie(id))
	http.Redirect(w, r, "/", 303)
}
func (h *router) api(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[1] != "sites" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	actor := "admin"
	if len(parts) == 2 {
		switch r.Method {
		case "GET":
			v, e := h.Sites.List(ctx)
			if e != nil {
				h.fail(w, e)
			} else {
				writeJSON(w, 200, v)
			}
		case "POST":
			var in struct{ Name, Slug string }
			if !decode(w, r, &in) {
				return
			}
			s, p, e := h.Sites.Create(ctx, actor, in.Name, in.Slug)
			if e != nil {
				h.fail(w, e)
			} else {
				writeJSON(w, 201, map[string]any{"site": s, "password": p})
			}
		default:
			w.WriteHeader(405)
		}
		return
	}
	id := parts[2]
	if len(parts) == 3 {
		switch r.Method {
		case "GET":
			v, e := h.Sites.Get(ctx, id)
			if e != nil {
				h.fail(w, e)
			} else {
				writeJSON(w, 200, v)
			}
		case "PATCH":
			var in struct{ Name, Slug string }
			if !decode(w, r, &in) {
				return
			}
			if e := h.Sites.Rename(ctx, actor, id, in.Name, in.Slug); e != nil {
				h.fail(w, e)
			} else {
				w.WriteHeader(204)
			}
		case "DELETE":
			var in struct{ Slug string }
			if !decode(w, r, &in) {
				return
			}
			if e := h.Sites.Delete(ctx, actor, id, in.Slug); e != nil {
				h.fail(w, e)
			} else {
				w.WriteHeader(204)
			}
		default:
			w.WriteHeader(405)
		}
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	if parts[3] == "releases" && r.Method == "GET" {
		v, e := h.Sites.Releases(ctx, id)
		if e != nil {
			h.fail(w, e)
		} else {
			writeJSON(w, 200, v)
		}
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var err error
	switch parts[3] {
	case "disable":
		err = h.Sites.Disable(ctx, actor, id)
	case "enable":
		err = h.Sites.Enable(ctx, actor, id)
	case "credentials":
		var in struct{ Username string }
		if !decode(w, r, &in) {
			return
		}
		p, e := h.Sites.RegenerateCredentials(ctx, actor, id, in.Username)
		if e != nil {
			h.fail(w, e)
		} else {
			writeJSON(w, 200, map[string]string{"password": p})
		}
		return
	case "deploy":
		v, e := h.upload(r, id)
		if e != nil {
			h.fail(w, e)
		} else {
			writeJSON(w, 200, v)
		}
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, err)
	} else {
		w.WriteHeader(204)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func (h *router) upload(r *http.Request, id string) (domain.Release, error) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return domain.Release{}, err
	}
	defer r.MultipartForm.RemoveAll()
	f, header, err := r.FormFile("file")
	if err != nil {
		return domain.Release{}, storage.ErrEmptyUpload
	}
	defer f.Close()
	if header.Size > h.Config.Limits.MaxZipBytes {
		return domain.Release{}, storage.ErrLimitExceeded
	}
	switch strings.ToLower(filepath.Ext(header.Filename)) {
	case ".html":
		return h.Sites.DeployHTML(r.Context(), "admin", id, f)
	case ".zip":
		tmp, err := os.CreateTemp(filepath.Join(h.Config.DataDir, "staging"), "incoming-*.zip")
		if err != nil {
			return domain.Release{}, err
		}
		defer os.Remove(tmp.Name())
		n, err := io.Copy(tmp, io.LimitReader(f, h.Config.Limits.MaxZipBytes+1))
		closeErr := tmp.Close()
		if err != nil {
			return domain.Release{}, err
		}
		if closeErr != nil {
			return domain.Release{}, closeErr
		}
		if n > h.Config.Limits.MaxZipBytes {
			return domain.Release{}, storage.ErrLimitExceeded
		}
		return h.Sites.DeployZip(r.Context(), "admin", id, tmp.Name())
	default:
		return domain.Release{}, storage.ErrBadLayout
	}
}
func render(w http.ResponseWriter, body string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.New("page").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Report hosting</title><style>body{font:16px system-ui;max-width:900px;margin:3rem auto;padding:1rem}input,button{padding:.6rem;margin:.4rem}label{display:block}article{padding:1rem;border:1px solid #ddd;margin:1rem 0}code{overflow-wrap:anywhere}</style></head><body>` + body + `</body></html>`))
	if err := t.Execute(w, data); err != nil {
		fmt.Fprintln(w, "render error")
	}
}
