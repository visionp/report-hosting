package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPasswordCredentials(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		pw, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 20 || strings.ContainsAny(pw, ": \n") || seen[pw] {
			t.Fatalf("bad generated password")
		}
		seen[pw] = true
	}
	dir := t.TempDir()
	pw, _ := NewPassword()
	if err := WriteHtpasswd(dir, "site-1", pw); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, ".htpasswd")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySSHA(string(data), pw) || VerifySSHA(string(data), "wrong") {
		t.Fatal("password verification")
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("invalid credential line")
	}
	for _, user := range []string{"a:b", "a b", "a\nb", "", "é"} {
		if err := WriteHtpasswd(dir, user, pw); err == nil {
			t.Fatalf("accepted %q", user)
		}
		after, _ := os.ReadFile(name)
		if string(after) != string(data) {
			t.Fatal("changed credentials after invalid username")
		}
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(name)
		if st.Mode().Perm() != 0640 {
			t.Fatal(st.Mode())
		}
	}
}

func TestHtpasswdAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := WriteHtpasswd(dir, "user", "password"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				result <- nil
				return
			default:
			}
			b, err := os.ReadFile(filepath.Join(dir, ".htpasswd"))
			if err != nil {
				result <- err
				return
			}
			if !VerifySSHA(string(b), "password") {
				result <- os.ErrInvalid
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		if err := WriteHtpasswd(dir, "user", "password"); err != nil {
			close(done)
			<-result
			t.Fatal(err)
		}
	}
	close(done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
