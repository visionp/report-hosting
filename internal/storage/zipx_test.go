package storage

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name, body string
	mode       os.FileMode
}

func archive(t *testing.T, entries ...entry) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "upload.zip")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func limits() Limits {
	return Limits{MaxZipBytes: 50 * 1024 * 1024, MaxExtractedBytes: 250 * 1024 * 1024, MaxFiles: 5000, MaxRatio: 100}
}

func TestZipValidation(t *testing.T) {
	cases := []struct {
		name    string
		entries []entry
		want    error
	}{
		{"parent", []entry{{"../x", "x", 0}}, ErrUnsafePath},
		{"absolute", []entry{{"/x", "x", 0}}, ErrUnsafePath},
		{"backslash", []entry{{"a\\b", "x", 0}}, ErrUnsafePath},
		{"nul", []entry{{"a\x00b", "x", 0}}, ErrUnsafePath},
		{"credentials", []entry{{".htpasswd", "x", 0}}, ErrUnsafePath},
		{"symlink", []entry{{"x", "target", os.ModeSymlink | 0777}}, ErrSpecialFile},
		{"device", []entry{{"x", "", os.ModeDevice | 0600}}, ErrSpecialFile},
		{"duplicate", []entry{{"Index.html", "x", 0}, {"index.html", "x", 0}}, ErrDuplicatePath},
		{"parent-case", []entry{{"Assets/a", "x", 0}, {"assets/b", "x", 0}}, ErrDuplicatePath},
		{"file-parent", []entry{{"x", "x", 0}, {"x/a", "x", 0}}, ErrDuplicatePath},
		{"two-dirs", []entry{{"a/index.html", "x", 0}, {"b/file", "x", 0}}, ErrBadLayout},
		{"nested", []entry{{"a/b/index.html", "x", 0}}, ErrNoIndex},
		{"index-dir", []entry{{"index.html/", "", os.ModeDir | 0750}}, ErrNoIndex},
		{"empty-index", []entry{{"index.html", "", 0}}, ErrEmptyUpload},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ExtractZip(archive(t, c.entries...), t.TempDir(), limits())
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

func TestZipLayoutsAndJunk(t *testing.T) {
	for _, prefix := range []string{"", "site/"} {
		t.Run(prefix, func(t *testing.T) {
			dir := t.TempDir()
			root, err := ExtractZip(archive(t, entry{prefix + "index.html", "hello", 0}, entry{prefix + "assets/a.css", "css", 0}, entry{"__MACOSX/x", "junk", 0}, entry{prefix + ".DS_Store", "junk", 0}), dir, limits())
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(dir, strings.TrimSuffix(prefix, "/"))
			if root != want {
				t.Fatalf("%s != %s", root, want)
			}
			b, err := os.ReadFile(filepath.Join(root, "index.html"))
			if err != nil || string(b) != "hello" {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "__MACOSX")); !os.IsNotExist(err) {
				t.Fatal("junk extracted")
			}
			if runtime.GOOS != "windows" {
				st, _ := os.Stat(filepath.Join(root, "index.html"))
				if st.Mode().Perm() != 0640 {
					t.Fatal(st.Mode())
				}
			}
		})
	}
}

func TestZipLimits(t *testing.T) {
	name := archive(t, entry{"index.html", strings.Repeat("x", 20000), 0}, entry{"other", "abc", 0})
	for _, mutate := range []func(*Limits){func(l *Limits) { l.MaxZipBytes = 1 }, func(l *Limits) { l.MaxExtractedBytes = 10 }, func(l *Limits) { l.MaxFiles = 1 }, func(l *Limits) { l.MaxRatio = 1 }} {
		l := limits()
		mutate(&l)
		if _, err := ExtractZip(name, t.TempDir(), l); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("got %v", err)
		}
	}
}

func TestStageHTML(t *testing.T) {
	for _, c := range []struct {
		body  string
		limit int64
		want  error
	}{{"hello", 5, nil}, {"hello", 4, ErrLimitExceeded}, {"", 10, ErrEmptyUpload}} {
		dir := t.TempDir()
		root, err := StageHTML(bytes.NewBufferString(c.body), dir, c.limit)
		if !errors.Is(err, c.want) {
			t.Fatalf("got %v", err)
		}
		if err == nil {
			b, _ := os.ReadFile(filepath.Join(root, "index.html"))
			if string(b) != c.body {
				t.Fatal("wrong HTML")
			}
		}
	}
}

func TestExtractionRejectsPreexistingStagingContents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractZip(archive(t, entry{"index.html", "hello", 0}), dir, limits()); !errors.Is(err, ErrBadLayout) {
		t.Fatal(err)
	}
}
