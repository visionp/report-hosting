package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/argon2"
	"report_hosting/internal/config"
	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrBadCredentials = errors.New("invalid credentials")
	ErrLocked         = errors.New("login temporarily locked")
	ErrSessionExpired = errors.New("session expired")
)

func validParams(p config.Argon2Params) bool {
	return p.Parallelism > 0 && p.Parallelism <= 16 && p.Memory >= 8*uint32(p.Parallelism) && p.Memory <= 256*1024 && p.Iterations > 0 && p.Iterations <= 10
}
func HashPassword(pw string, p config.Argon2Params) (string, error) {
	if !validParams(p) || len(pw) == 0 || len(pw) > 4096 {
		return "", errors.New("invalid password or Argon2 parameters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", p.Memory, p.Iterations, p.Parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}
func VerifyPassword(hash, pw string) bool {
	if len(pw) > 4096 || len(hash) > 512 {
		return false
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	var nums [3]uint64
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], prefix) {
			return false
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(params[i], prefix), 10, 32)
		if err != nil {
			return false
		}
		nums[i] = v
	}
	if nums[2] > 255 {
		return false
	}
	p := config.Argon2Params{Memory: uint32(nums[0]), Iterations: uint32(nums[1]), Parallelism: uint8(nums[2])}
	if !validParams(p) {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

const (
	failureWindow = 15 * time.Minute
	sessionIdle   = 30 * time.Minute
	purgeEvery    = 10 * time.Minute
)

type Session = repository.Session
type Auth struct {
	Store  *repository.Store
	Now    func() time.Time
	Params config.Argon2Params
	mu     sync.Mutex
	// lastPurge is guarded by mu.
	lastPurge time.Time
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}
func sessionHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (a *Auth) Login(ctx context.Context, username, password, ip string) (string, error) {
	if len(username) > 32 || len(password) > 4096 || len(ip) > 64 || username == "" {
		return "", ErrBadCredentials
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	// Failure rows are keyed by attacker-chosen usernames and sessions are only
	// deleted when presented again, so sweep stale rows periodically.
	if now.Sub(a.lastPurge) >= purgeEvery {
		if err := a.Store.PurgeStale(ctx, now, failureWindow, sessionIdle); err != nil {
			return "", err
		}
		a.lastPurge = now
	}
	keys := []string{"ip:" + ip, "account:" + username}
	for _, key := range keys {
		until, err := a.Store.LockedUntil(ctx, key)
		if err != nil {
			return "", err
		}
		if now.Before(until) {
			return "", ErrLocked
		}
	}
	admin, err := a.Store.GetAdmin(ctx, username)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	valid := err == nil && VerifyPassword(admin.PasswordHash, password)
	if errors.Is(err, domain.ErrNotFound) {
		p := a.Params
		if !validParams(p) {
			p = config.Defaults().Argon2
		}
		// Match the expensive password work even when the account is absent.
		argon2.IDKey([]byte(password), make([]byte, 16), p.Iterations, p.Memory, p.Parallelism, 32)
	}
	if !valid {
		for _, key := range keys {
			count, err := a.Store.RecordFailure(ctx, key, now, failureWindow)
			if err != nil {
				return "", err
			}
			if count >= 5 {
				if err := a.Store.Lock(ctx, key, now.Add(failureWindow)); err != nil {
					return "", err
				}
			}
		}
		return "", ErrBadCredentials
	}
	for _, key := range keys {
		if err := a.Store.ClearFailures(ctx, key); err != nil {
			return "", err
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	if err := a.Store.PutSession(ctx, sessionHash(id), now, now, now.Add(12*time.Hour)); err != nil {
		return "", err
	}
	return id, nil
}
func (a *Auth) Validate(ctx context.Context, id string) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := base64.RawURLEncoding.Strict().DecodeString(id)
	if err != nil || len(b) != 32 {
		return Session{}, ErrSessionExpired
	}
	s, err := a.Store.GetSession(ctx, sessionHash(id))
	if errors.Is(err, domain.ErrNotFound) {
		return Session{}, ErrSessionExpired
	}
	if err != nil {
		return Session{}, err
	}
	now := a.now()
	if !now.Before(s.ExpiresAt) || !now.Before(s.LastSeenAt.Add(sessionIdle)) {
		if err := a.Store.DeleteSession(ctx, s.IDHash); err != nil {
			return Session{}, err
		}
		return Session{}, ErrSessionExpired
	}
	if err := a.Store.TouchSession(ctx, s.IDHash, now); err != nil {
		return Session{}, err
	}
	s.LastSeenAt = now
	return s, nil
}
func (a *Auth) Logout(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Store.DeleteSession(ctx, sessionHash(id))
}
