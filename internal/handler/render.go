package handler

import (
	"bytes"
	"html/template"
	"net/http"

	"report_hosting/web"
)

// Each page has its own content definition and shares the same layout.
var pages = map[string]*template.Template{
	"login":     template.Must(template.ParseFS(web.Templates, "templates/layout.html", "templates/login.html")),
	"dashboard": template.Must(template.ParseFS(web.Templates, "templates/layout.html", "templates/dashboard.html")),
}

func render(w http.ResponseWriter, page string, data any) {
	var body bytes.Buffer
	if err := pages[page].ExecuteTemplate(&body, "layout", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = body.WriteTo(w)
}
