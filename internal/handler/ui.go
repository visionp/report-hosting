package handler

import (
	"errors"
	"fmt"
	"net/http"
	"report_hosting/internal/domain"
	"strings"
)

type dashboardView struct {
	Sites                                                                     []domain.Site
	CSRF, Password, Error, Query, Status, CreateName, CreateSlug, UploadLimit string
	Total, Active, Disabled                                                   int
	Detail                                                                    bool
}

func formError(err error) string {
	switch {
	case errors.Is(err, domain.ErrDuplicateSlug):
		return "This address is already in use. Please choose another one."
	case errors.Is(err, domain.ErrNameRequired):
		return "Give your site a name before continuing."
	case errors.Is(err, domain.ErrSlugConfirmation):
		return "The confirmation does not match the site address. Nothing was deleted."
	case errors.Is(err, domain.ErrInvalidSlug), errors.Is(err, domain.ErrReservedSlug):
		return "Choose an available address using lowercase letters, numbers and single hyphens."
	case errors.Is(err, domain.ErrInvalidUsername):
		return "Use 1–32 lowercase letters, numbers or hyphens for the site username."
	case errors.Is(err, domain.ErrSiteNotDisabled):
		return "Disable the website before deleting it permanently."
	}
	_, code := errorResponse(err)
	switch code {
	case "upload_too_large":
		return "This upload exceeds the allowed size. Please choose a smaller file."
	case "insufficient_storage":
		return "There is not enough storage to publish this upload. Your current release is unchanged."
	case "invalid_input":
		return "Please check your input. Upload a valid ZIP with index.html, or a non-empty HTML file."
	case "not_found":
		return "This site could not be found. It may have been deleted."
	default:
		return "We could not complete this action. Please try again."
	}
}

func (h *router) ui(w http.ResponseWriter, r *http.Request, token string) {
	password := ""
	view := dashboardView{CSRF: token, Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status")}
	view.UploadLimit = fmt.Sprintf("%g MiB", float64(h.Config.Limits.MaxZipBytes)/(1<<20))
	status := http.StatusOK
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
			status, _ = errorResponse(err)
			view.Error = formError(err)
			if r.URL.Path == "/sites/create" {
				view.CreateName, view.CreateSlug = r.FormValue("name"), r.FormValue("slug")
			}
		}
		if password == "" && err == nil {
			http.Redirect(w, r, "/", 303)
			return
		}
	} else if r.Method != "GET" || (r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/sites/")) {
		http.NotFound(w, r)
		return
	}
	sites, err := h.Sites.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	view.Total = len(sites)
	for _, site := range sites {
		if site.Status == domain.SiteActive {
			view.Active++
		} else {
			view.Disabled++
		}
	}
	if view.Status != "active" && view.Status != "disabled" {
		view.Status = ""
	}
	query := strings.ToLower(strings.TrimSpace(view.Query))
	for _, site := range sites {
		if view.Status != "" && string(site.Status) != view.Status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(site.Name+" "+site.Slug), query) {
			continue
		}
		view.Sites = append(view.Sites, site)
	}
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/sites/") {
		var site domain.Site
		site, err = h.Sites.Get(r.Context(), strings.TrimPrefix(r.URL.Path, "/sites/"))
		view.Sites = []domain.Site{site}
		view.Detail = true
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	view.Password = password
	renderStatus(w, "dashboard", view, status)
}
