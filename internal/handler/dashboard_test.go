package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func submitForm(h http.Handler, path, session string, values url.Values) *httptest.ResponseRecorder {
	if session != "" {
		values.Set("csrf", csrf(session))
	}
	r := httptest.NewRequest("POST", "https://admin.partnersflex.space"+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://admin.partnersflex.space")
	if session != "" {
		r.AddCookie(SessionCookie(session))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWebsiteLibrarySearchAndStatusFilter(t *testing.T) {
	h, session := testRouter(t)
	for _, slug := range []string{"alpha", "beta"} {
		w := request(h, "POST", "/api/sites", `{"name":"Report `+slug+`","slug":"`+slug+`"}`, session, "https://admin.partnersflex.space", csrf(session))
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
		if slug == "beta" {
			var created struct{ Site struct{ ID string } }
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			w = request(h, "POST", "/api/sites/"+created.Site.ID+"/disable", "", session, "https://admin.partnersflex.space", csrf(session))
			if w.Code != 204 {
				t.Fatal(w.Body.String())
			}
		}
	}
	for _, tc := range []struct{ query, include, exclude string }{
		{"?q=ALPHA", "Report alpha", "Report beta"},
		{"?status=active", "Report alpha", "Report beta"},
		{"?status=disabled", "Report beta", "Report alpha"},
		{"?q=alpha&status=disabled", "No matching websites", "Report alpha"},
	} {
		w := request(h, "GET", "/"+tc.query, "", session, "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.include) || strings.Contains(w.Body.String(), tc.exclude) {
			t.Fatalf("filter %s: %d %s", tc.query, w.Code, w.Body.String())
		}
	}
}

func TestWebsiteFormErrorKeepsLayoutAndEnteredValues(t *testing.T) {
	h, session := testRouter(t)
	values := url.Values{"name": {"Saved report"}, "slug": {"taken"}}
	if w := submitForm(h, "/sites/create", session, values); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	values.Set("name", "Keep my input")
	w := submitForm(h, "/sites/create", session, values)
	if w.Code != 409 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(w.Body.String(), "This address is already in use") ||
		!strings.Contains(w.Body.String(), `value="Keep my input"`) ||
		!strings.Contains(w.Body.String(), `role="alert"`) {
		t.Fatalf("form error should retain the workspace and inputs: %d %s", w.Code, w.Body.String())
	}
}

func TestLoginErrorStaysOnSignInPage(t *testing.T) {
	h, _ := testRouter(t)
	w := submitForm(h, "/login", "", url.Values{"username": {"admin"}, "password": {"wrong"}})
	if w.Code != 401 || !strings.Contains(w.Body.String(), "The username or password is incorrect") || !strings.Contains(w.Body.String(), `autocomplete="current-password"`) {
		t.Fatalf("login error lost the sign-in form: %d %s", w.Code, w.Body.String())
	}
}
