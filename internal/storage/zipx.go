package storage

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"report_hosting/internal/config"
)

var (
	ErrUnsafePath    = errors.New("unsafe archive path")
	ErrSpecialFile   = errors.New("archive contains a special file")
	ErrDuplicatePath = errors.New("duplicate archive path")
	ErrLimitExceeded = errors.New("upload limit exceeded")
	ErrNoIndex       = errors.New("index.html is missing or not a regular file")
	ErrBadLayout     = errors.New("unsupported archive layout")
	ErrEmptyUpload   = errors.New("empty upload")
)

type Limits = config.Limits

type zipEntry struct {
	file *zip.File
	name string
	dir  bool
}

func archiveName(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return "", ErrUnsafePath
	}
	name = strings.TrimSuffix(name, "/")
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" {
			return "", ErrUnsafePath
		}
		if strings.EqualFold(part, ".htpasswd") {
			return "", ErrUnsafePath
		}
	}
	return name, nil
}

func ignored(name string) bool {
	for _, p := range strings.Split(name, "/") {
		if p == "__MACOSX" || p == ".DS_Store" {
			return true
		}
	}
	return false
}

// emptyStaging requires a private, freshly created directory. This prevents a
// pre-existing symlink or file from redirecting extraction outside its root.
func emptyStaging(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(files) != 0 {
		return ErrBadLayout
	}
	return nil
}

func ExtractZip(zipPath, stagingDir string, lim Limits) (string, error) {
	if lim.MaxZipBytes <= 0 || lim.MaxExtractedBytes <= 0 || lim.MaxFiles <= 0 || lim.MaxRatio <= 0 {
		return "", ErrLimitExceeded
	}
	if err := emptyStaging(stagingDir); err != nil {
		return "", err
	}
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer z.Close()
	st, err := os.Stat(zipPath)
	if err != nil {
		return "", err
	}
	if st.Size() > lim.MaxZipBytes {
		return "", ErrLimitExceeded
	}
	var entries []zipEntry
	seen := map[string]bool{}
	kinds := map[string]bool{}
	files := 0
	for _, f := range z.File {
		name, err := archiveName(f.Name)
		if err != nil {
			return "", err
		}
		if ignored(name) {
			continue
		}
		mode := f.Mode()
		dir := mode.IsDir()
		if !dir && !mode.IsRegular() {
			return "", ErrSpecialFile
		}
		key := strings.ToLower(name)
		if seen[key] {
			return "", ErrDuplicatePath
		}
		seen[key] = true
		kinds[key] = dir
		// Directory records also consume validation memory; count every entry.
		files++
		if files > lim.MaxFiles {
			return "", ErrLimitExceeded
		}
		entries = append(entries, zipEntry{f, name, dir})
	}
	// Catch file/directory collisions, including implicit and case-folded parents,
	// before writing the first entry.
	spellings := map[string]string{}
	for _, e := range entries {
		for p := e.name; p != "."; p = path.Dir(p) {
			key := strings.ToLower(p)
			if old, ok := spellings[key]; ok && old != p {
				return "", ErrDuplicatePath
			}
			spellings[key] = p
			if p != e.name {
				if dir, ok := kinds[key]; ok && !dir {
					return "", ErrDuplicatePath
				}
			}
		}
	}
	root := ""
	if dir, ok := kinds["index.html"]; ok && !dir && spellings["index.html"] == "index.html" {
		root = stagingDir
	} else {
		top := ""
		for _, e := range entries {
			part := strings.Split(e.name, "/")[0]
			if top == "" {
				top = part
			}
			if part != top {
				return "", ErrBadLayout
			}
			if e.name == part && !e.dir {
				return "", ErrNoIndex
			}
		}
		if top == "" {
			return "", ErrNoIndex
		}
		idx := top + "/index.html"
		if dir, ok := kinds[strings.ToLower(idx)]; !ok || dir || spellings[strings.ToLower(idx)] != idx {
			return "", ErrNoIndex
		}
		root = filepath.Join(stagingDir, top)
	}
	remaining := lim.MaxExtractedBytes
	// Use archive bytes as a denominator, never untrusted size declarations.
	if st.Size() <= lim.MaxExtractedBytes/int64(lim.MaxRatio) {
		remaining = st.Size() * int64(lim.MaxRatio)
	}
	for _, e := range entries {
		dst := filepath.Join(stagingDir, filepath.FromSlash(e.name))
		if e.dir {
			if err := os.MkdirAll(dst, 0750); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
			return "", err
		}
		r, err := e.file.Open()
		if err != nil {
			return "", err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
		if err != nil {
			r.Close()
			return "", err
		}
		n, copyErr := io.Copy(f, io.LimitReader(r, remaining+1))
		readErr := r.Close()
		writeErr := f.Close()
		remaining -= n
		if remaining < 0 {
			return "", ErrLimitExceeded
		}
		if copyErr != nil {
			return "", copyErr
		}
		if readErr != nil {
			return "", readErr
		}
		if writeErr != nil {
			return "", writeErr
		}
	}
	idx, err := os.Stat(filepath.Join(root, "index.html"))
	if err != nil {
		return "", err
	}
	if idx.Size() == 0 {
		return "", ErrEmptyUpload
	}
	return root, nil
}

func StageHTML(r io.Reader, stagingDir string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", ErrLimitExceeded
	}
	if err := emptyStaging(stagingDir); err != nil {
		return "", err
	}
	name := filepath.Join(stagingDir, "index.html")
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, maxBytes+1))
	closeErr := f.Close()
	if n > maxBytes {
		os.Remove(name)
		return "", ErrLimitExceeded
	}
	if copyErr != nil {
		os.Remove(name)
		return "", copyErr
	}
	if closeErr != nil {
		os.Remove(name)
		return "", closeErr
	}
	if n == 0 {
		os.Remove(name)
		return "", ErrEmptyUpload
	}
	return stagingDir, nil
}
