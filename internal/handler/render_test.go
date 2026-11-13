package handler

import (
	"strings"
	"testing"
)

func TestLoginLoadsSharedStylesWithoutSession(t *testing.T) {
	h, _ := testRouter(t)
	page := request(h, "GET", "/login", "", "", "", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/static/styles.css"`) || strings.Contains(page.Body.String(), "<style>") {
		t.Fatalf("login must use the shared stylesheet: %d %s", page.Code, page.Body.String())
	}
	css := request(h, "GET", "/static/styles.css", "", "", "", "")
	if css.Code != 200 || css.Header().Get("Content-Type") != "text/css; charset=utf-8" || css.Body.Len() == 0 {
		t.Fatalf("stylesheet must load before login: %d %s", css.Code, css.Body.String())
	}
}
