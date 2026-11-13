package storage

import (
	"crypto/rand"
	"crypto/sha1" // Nginx's SSHA wire format; passwords are generated with 120 bits of entropy.
	"crypto/subtle"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"report_hosting/internal/domain"
)

func NewPassword() (string, error) {
	var b [15]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// WriteHtpasswd replaces credentials atomically; it never truncates the live file.
func WriteHtpasswd(siteDir, username, password string) error {
	if err := domain.ValidateUsername(username); err != nil {
		return err
	}
	var salt [8]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return err
	}
	h := sha1.New()
	h.Write([]byte(password))
	h.Write(salt[:])
	encoded := base64.StdEncoding.EncodeToString(append(h.Sum(nil), salt[:]...))
	f, err := os.CreateTemp(siteDir, ".htpasswd-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0640); err == nil {
		_, err = f.WriteString(username + ":{SSHA}" + encoded + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(siteDir, ".htpasswd"))
}

func VerifySSHA(line, password string) bool {
	_, hash, ok := strings.Cut(strings.TrimSuffix(line, "\n"), ":{SSHA}")
	if !ok {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(hash)
	if err != nil || len(b) <= sha1.Size {
		return false
	}
	h := sha1.New()
	h.Write([]byte(password))
	h.Write(b[sha1.Size:])
	return subtle.ConstantTimeCompare(h.Sum(nil), b[:sha1.Size]) == 1
}
