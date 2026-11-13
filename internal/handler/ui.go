package handler

import (
	"net/http"
	"report_hosting/internal/domain"
	"strings"
)

func (h *router) ui(w http.ResponseWriter, r *http.Request, token string) {
	password := ""
	var err error
	if r.Method == "POST" {
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(p) == 2 && p[0] == "sites" && p[1] == "create" {
			_, password, err = h.Sites.Create(r.Context(), "admin", r.FormValue("name"), r.FormValue("slug"))
		} else if len(p) == 3 && p[0] == "sites" {
			switch p[2] {
			case "rename":
				err = h.Sites.Rename(r.Context(), "admin", p[1], r.FormValue("name"), r.FormValue("slug"))
			case "disable":
				err = h.Sites.Disable(r.Context(), "admin", p[1])
			case "enable":
				err = h.Sites.Enable(r.Context(), "admin", p[1])
			case "delete":
				err = h.Sites.Delete(r.Context(), "admin", p[1], r.FormValue("slug"))
			case "credentials":
				password, err = h.Sites.RegenerateCredentials(r.Context(), "admin", p[1], r.FormValue("username"))
			case "deploy":
				_, err = h.upload(r, p[1])
			default:
				http.NotFound(w, r)
				return
			}
		} else {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			h.fail(w, err)
			return
		}
		if password == "" {
			http.Redirect(w, r, "/", 303)
			return
		}
	} else if r.Method != "GET" || (r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/sites/")) {
		http.NotFound(w, r)
		return
	}
	sites, err := h.Sites.List(r.Context())
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/sites/") {
		var site domain.Site
		site, err = h.Sites.Get(r.Context(), strings.TrimPrefix(r.URL.Path, "/sites/"))
		sites = []domain.Site{site}
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	render(w, "dashboard", struct {
		Sites          []domain.Site
		CSRF, Password string
	}{sites, token, password})
}
