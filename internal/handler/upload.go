package handler

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"report_hosting/internal/domain"
	"report_hosting/internal/storage"
)

// uploadSource is the uploaded file: a stream, or (when the multipart body was
// already spooled to disk by the CSRF check) the spooled file's path.
type uploadSource struct {
	name string
	r    io.Reader
	path string // set when r is a disk file that can be used in place
	size int64  // -1 when unknown
}

func (h *router) uploadSource(r *http.Request) (src uploadSource, closeFn func(), err error) {
	closeFn = func() {}
	if r.MultipartForm != nil {
		// Already parsed (form-based CSRF token); reuse the spooled copy instead of re-reading.
		f, header, err := r.FormFile("file")
		if err != nil {
			return src, closeFn, storage.ErrEmptyUpload
		}
		src = uploadSource{name: header.Filename, r: f, size: header.Size}
		if osf, ok := f.(*os.File); ok {
			src.path = osf.Name()
		}
		return src, func() { f.Close() }, nil
	}
	// Header-token requests are streamed straight from the request body: no
	// multipart temp file in os.TempDir and no second copy.
	mr, err := r.MultipartReader()
	if err != nil {
		return src, closeFn, storage.ErrEmptyUpload
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return src, closeFn, storage.ErrEmptyUpload
		}
		if err != nil {
			return src, closeFn, err
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return uploadSource{name: part.FileName(), r: part, size: -1}, func() { part.Close() }, nil
		}
	}
}

func (h *router) upload(r *http.Request, id string) (domain.Release, error) {
	src, closeSrc, err := h.uploadSource(r)
	if err != nil {
		return domain.Release{}, err
	}
	defer closeSrc()
	max := h.Config.Limits.MaxZipBytes
	if src.size > max {
		return domain.Release{}, storage.ErrLimitExceeded
	}
	switch strings.ToLower(filepath.Ext(src.name)) {
	case ".html":
		return h.Sites.DeployHTML(r.Context(), "admin", id, src.r)
	case ".zip":
		if src.path != "" {
			return h.Sites.DeployZip(r.Context(), "admin", id, src.path)
		}
		tmp, err := os.CreateTemp(filepath.Join(h.Config.DataDir, "staging"), "incoming-*.zip")
		if err != nil {
			return domain.Release{}, err
		}
		defer os.Remove(tmp.Name())
		n, err := io.Copy(tmp, io.LimitReader(src.r, max+1))
		closeErr := tmp.Close()
		if err != nil {
			return domain.Release{}, err
		}
		if closeErr != nil {
			return domain.Release{}, closeErr
		}
		if n > max {
			return domain.Release{}, storage.ErrLimitExceeded
		}
		return h.Sites.DeployZip(r.Context(), "admin", id, tmp.Name())
	default:
		return domain.Release{}, storage.ErrBadLayout
	}
}
