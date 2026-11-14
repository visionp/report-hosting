package handler

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"report_hosting/web"
)

// Each page has its own content definition and shares the same layout.
var pages = map[string]*template.Template{
	"login":       pageTemplate("login"),
	"dashboard":   pageTemplate("dashboard"),
	"create":      pageTemplate("create"),
	"site":        pageTemplate("site"),
	"credentials": pageTemplate("credentials"),
}

// Keep the current search when switching between status filters.
func libraryURL(query, status string) string {
	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	if status != "" {
		params.Set("status", status)
	}
	u := url.URL{Path: "/", RawQuery: params.Encode()}
	return u.String()
}

func pageTemplate(name string) *template.Template {
	files := []string{"templates/layout.html", "templates/icons.html"}
	if name != "login" {
		files = append(files, "templates/workspace.html")
	}
	files = append(files, "templates/"+name+".html")
	return template.Must(template.New("layout").Funcs(template.FuncMap{
		"libraryURL": libraryURL,
		"initial": func(s string) string {
			for _, r := range strings.TrimSpace(s) {
				return strings.ToUpper(string(r))
			}
			return "R"
		},
		"date": func(t time.Time) string { return t.UTC().Format("02 Jan 2006 · 15:04 UTC") },
	}).ParseFS(web.Templates, files...))
}

func render(w http.ResponseWriter, page string, data any) {
	renderStatus(w, page, data, http.StatusOK)
}

func renderStatus(w http.ResponseWriter, page string, data any, status int) {
	var body bytes.Buffer
	if err := pages[page].ExecuteTemplate(&body, "layout", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}
