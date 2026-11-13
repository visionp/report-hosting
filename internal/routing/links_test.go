package routing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameOrderAndRollback(t *testing.T) {
	l := Links{Dir: filepath.Join(t.TempDir(), "by-slug")}
	if err := l.Create("old", "site-1"); err != nil {
		t.Fatal(err)
	}
	err := l.Rename("old", "new", "site-1", func() error {
		v, e := l.List()
		if e != nil {
			t.Fatal(e)
		}
		if len(v) != 2 {
			t.Fatal(v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(l.Dir, "old")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	failure := errors.New("commit failed")
	if err = l.Rename("new", "next", "site-1", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	v, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 || v["new"] != "site-1" {
		t.Fatal(v)
	}
}
func TestLinkCannotOverwriteOrEscape(t *testing.T) {
	l := Links{Dir: t.TempDir()}
	if err := l.Create("demo", "site-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Create("demo", "site-2"); err == nil {
		t.Fatal("route overwritten")
	}
	if err := l.Create("other", "../escape"); err == nil {
		t.Fatal("unsafe ID accepted")
	}
	if err := l.Remove("../demo"); err == nil {
		t.Fatal("unsafe removal accepted")
	}
}
