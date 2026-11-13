package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// A forged central-directory size must not turn a corrupt ZIP into a release.
func TestZipForgedSizeRejected(t *testing.T) {
	name := archive(t, entry{"index.html", "<html>complete content</html>", 0})
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte{'P', 'K', 1, 2})
	if i < 0 {
		t.Fatal("central directory missing")
	}
	binary.LittleEndian.PutUint32(b[i+24:i+28], 1)
	if err := os.WriteFile(name, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractZip(name, t.TempDir(), limits()); err == nil {
		t.Fatal("accepted forged uncompressed size")
	}
}

func TestStagingSymlinkCannotEscape(t *testing.T) {
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "staging")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ExtractZip(archive(t, entry{"index.html", "hello", 0}), link, limits()); err == nil {
		t.Fatal("accepted staging symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside staging", err)
	}
}
