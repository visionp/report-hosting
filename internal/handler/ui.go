package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"report_hosting/internal/domain"
)

type workspaceView struct {
	CSRF       string
	BaseDomain string
}

type libraryView struct {
	workspaceView
	Sites                   []domain.Site
	Query, Status           string
	Total, Active, Disabled int
}

type createView struct {
	workspaceView
	Name, Slug, Error string
}

type siteView struct {
	workspaceView
	Site                 domain.Site
	UploadLimit          string
	Name, Slug, Username string
	FailedAction, Error  string
}

type credentialsView struct {
	workspaceView
	Site            domain.Site
	Title, Password string
	Error           string
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
		return "Use 1-32 lowercase letters, numbers or hyphens for the site username."
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
	workspace := workspaceView{CSRF: token, BaseDomain: h.Config.BaseDomain}
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		h.library(w, r, workspace)
		return
	}
	if r.URL.Path == "/sites/create" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		h.createSite(w, r, workspace)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/sites/")
	id, action, hasAction := strings.Cut(path, "/")
	if ok && id != "" {
		switch {
		case r.Method == http.MethodGet && !hasAction:
			h.sitePage(w, r, workspace, id, "")
			return
		case r.Method == http.MethodPost && hasAction && action != "":
			h.sitePage(w, r, workspace, id, action)
			return
		}
	}
	http.NotFound(w, r)
}

func (h *router) library(w http.ResponseWriter, r *http.Request, workspace workspaceView) {
	sites, err := h.Sites.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	params := r.URL.Query()
	view := libraryView{workspaceView: workspace, Query: params.Get("q"), Status: params.Get("status"), Total: len(sites)}
	if view.Status != "active" && view.Status != "disabled" {
		view.Status = ""
	}
	query := strings.ToLower(strings.TrimSpace(view.Query))
	for _, site := range sites {
		if site.Status == domain.SiteActive {
			view.Active++
		} else {
			view.Disabled++
		}
		if view.Status != "" && string(site.Status) != view.Status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(site.Name+" "+site.Slug), query) {
			continue
		}
		view.Sites = append(view.Sites, site)
	}
	render(w, "dashboard", view)
}

func (h *router) createSite(w http.ResponseWriter, r *http.Request, workspace workspaceView) {
	view := createView{workspaceView: workspace}
	if r.Method == http.MethodGet {
		render(w, "create", view)
		return
	}
	view.Name, view.Slug = r.FormValue("name"), r.FormValue("slug")
	site, password, err := h.Sites.Create(r.Context(), "admin", view.Name, view.Slug)
	if password != "" {
		showCredentials(w, workspace, site, "Website created", password, err)
		return
	}
	status, _ := errorResponse(err)
	view.Error = formError(err)
	renderStatus(w, "create", view, status)
}

// A password is rendered only in the response to the action that generated it.
func showCredentials(
	w http.ResponseWriter,
	workspace workspaceView,
	site domain.Site,
	title, password string,
	err error,
) {
	view := credentialsView{workspaceView: workspace, Site: site, Title: title, Password: password}
	status := http.StatusOK
	if err != nil {
		// The service can return the new credentials together with an audit write error.
		status, _ = errorResponse(err)
		view.Error = "Your credentials were saved, but the action could not be recorded."
	}
	renderStatus(w, "credentials", view, status)
}

func (h *router) sitePage(w http.ResponseWriter, r *http.Request, workspace workspaceView, id, action string) {
	ctx := r.Context()
	site, err := h.Sites.Get(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			h.fail(w, err)
		}
		return
	}
	view := siteView{
		workspaceView: workspace, Site: site,
		Name: site.Name, Slug: site.Slug, Username: site.AuthUsername,
		UploadLimit: fmt.Sprintf("%g MiB", float64(h.Config.Limits.MaxZipBytes)/(1<<20)),
	}
	if action == "" {
		render(w, "site", view)
		return
	}

	destination := "/sites/" + site.ID
	switch action {
	case "deploy":
		_, err = h.upload(r, id)
	case "rename":
		view.Name, view.Slug = r.FormValue("name"), r.FormValue("slug")
		err = h.Sites.Rename(ctx, "admin", id, view.Name, view.Slug)
	case "credentials":
		view.Username = r.FormValue("username")
		var password string
		password, err = h.Sites.RegenerateCredentials(ctx, "admin", id, view.Username)
		if password != "" {
			site.AuthUsername = view.Username
			showCredentials(w, workspace, site, "New website password", password, err)
			return
		}
	case "disable":
		err = h.Sites.Disable(ctx, "admin", id)
	case "enable":
		err = h.Sites.Enable(ctx, "admin", id)
	case "delete":
		err = h.Sites.Delete(ctx, "admin", id, r.FormValue("slug"))
		destination = "/"
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		status, _ := errorResponse(err)
		view.FailedAction, view.Error = action, formError(err)
		renderStatus(w, "site", view, status)
		return
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
