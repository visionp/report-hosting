package handler

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"report_hosting/internal/domain"
	"report_hosting/internal/storage"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}
func (h *router) fail(w http.ResponseWriter, err error) {
	status, code := errorResponse(err)
	writeJSON(w, status, map[string]string{"error": code})
}

func errorResponse(err error) (int, string) {
	status, code := 500, "internal_error"
	var large *http.MaxBytesError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status, code = 404, "not_found"
	case errors.As(err, &large), errors.Is(err, storage.ErrLimitExceeded):
		status, code = 413, "upload_too_large"
	case errors.Is(err, storage.ErrInsufficientDisk):
		status, code = 507, "insufficient_storage"
	case errors.Is(err, domain.ErrDuplicateSlug):
		status, code = 409, "duplicate_slug"
	case errors.Is(err, domain.ErrInvalidSlug), errors.Is(err, domain.ErrReservedSlug), errors.Is(err, domain.ErrInvalidUsername), errors.Is(err, domain.ErrNameRequired), errors.Is(err, zip.ErrFormat), errors.Is(err, domain.ErrSlugConfirmation), errors.Is(err, domain.ErrSiteNotDisabled), errors.Is(err, storage.ErrUnsafePath), errors.Is(err, storage.ErrSpecialFile), errors.Is(err, storage.ErrDuplicatePath), errors.Is(err, storage.ErrNoIndex), errors.Is(err, storage.ErrBadLayout), errors.Is(err, storage.ErrEmptyUpload):
		status, code = 400, "invalid_input"
	}
	return status, code
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
