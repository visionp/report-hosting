package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"report_hosting/internal/config"
	"report_hosting/internal/repository"
	"strings"
	"testing"
	"time"
)

func testAuth(t *testing.T) (*Auth, *time.Time) {
	t.Helper()
	st, err := repository.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	p := config.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1}
	h, err := HashPassword("correct-password", p)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateAdmin(context.Background(), "admin", h); err != nil {
		t.Fatal(err)
	}
	return &Auth{Store: st, Params: p, Now: func() time.Time { return now }}, &now
}
func TestHashPassword(t *testing.T) {
	h, err := HashPassword("secret", config.Defaults().Argon2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "m=65536,t=3,p=2") || !VerifyPassword(h, "secret") || VerifyPassword(h, "wrong") {
		t.Fatal("hash contract failed")
	}
	for _, h := range []string{"", "$argon2id$v=19$m=4294967295,t=3,p=2$a$b", "$argon2id$v=19$m=65536,t=3,p=258$a$b"} {
		if VerifyPassword(h, "secret") {
			t.Fatal("invalid hash accepted")
		}
	}
}
func TestSessionLifecycle(t *testing.T) {
	a, now := testAuth(t)
	ctx := context.Background()
	id, err := a.Login(ctx, "admin", "correct-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Login(ctx, "admin", "correct-password", "127.0.0.1")
	if err != nil || second == id {
		t.Fatal("session ID not rotated")
	}
	s, err := a.Store.GetSession(ctx, sessionHash(id))
	if err != nil || s.IDHash == id {
		t.Fatal("session not hashed")
	}
	*now = now.Add(29 * time.Minute)
	if _, err := a.Validate(ctx, id); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(30 * time.Minute)
	if _, err := a.Validate(ctx, id); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want expired, got %v", err)
	}
	id, err = a.Login(ctx, "admin", "correct-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Logout(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Validate(ctx, id); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("logout did not invalidate")
	}
}
func TestAbsoluteExpiry(t *testing.T) {
	a, now := testAuth(t)
	ctx := context.Background()
	id, err := a.Login(ctx, "admin", "correct-password", "ip")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 35; i++ {
		*now = now.Add(20 * time.Minute)
		if _, err := a.Validate(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(20 * time.Minute)
	if _, err := a.Validate(ctx, id); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("absolute expiration not enforced")
	}
}
func TestLockoutPerAddressAndAccount(t *testing.T) {
	for _, mode := range []string{"address", "account"} {
		t.Run(mode, func(t *testing.T) {
			a, now := testAuth(t)
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				user, ip := "admin", "ip"
				if mode == "address" {
					user = strings.Repeat("x", i+1)
				} else {
					ip = strings.Repeat("x", i+1)
				}
				if _, err := a.Login(ctx, user, "wrong", ip); !errors.Is(err, ErrBadCredentials) {
					t.Fatal(err)
				}
			}
			if _, err := a.Login(ctx, "admin", "correct-password", "ip"); !errors.Is(err, ErrLocked) {
				t.Fatalf("not locked: %v", err)
			}
			*now = now.Add(15 * time.Minute)
			if _, err := a.Login(ctx, "admin", "correct-password", "ip"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoginPurgesStaleAttemptsAndSessions(t *testing.T) {
	a, now := testAuth(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		if _, err := a.Login(ctx, "bogus"+string(rune('a'+i)), "x", fmt.Sprintf("10.1.0.%d", i)); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	id, err := a.Login(ctx, "admin", "correct-password", "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(13 * time.Hour) // past failure window, idle timeout and session expiry
	if _, err = a.Login(ctx, "admin", "correct-password", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.GetSession(ctx, sessionHash(id)); err == nil {
		t.Fatal("expired session was not purged")
	}
	if until, err := a.Store.LockedUntil(ctx, "account:boguse"); err != nil || !until.IsZero() {
		t.Fatalf("stale lock state: %v %v", until, err)
	}
	if err := a.Store.PurgeStale(ctx, *now, failureWindow, sessionIdle); err != nil {
		t.Fatal(err)
	}
}
