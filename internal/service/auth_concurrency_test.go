package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoginWaitsForGlobalCapacityAndCanCancel(t *testing.T) {
	for _, username := range []string{"admin", "unknown-user"} {
		t.Run(username, func(t *testing.T) {
			a, _ := testAuth(t)
			id, err := a.Login(context.Background(), "admin", "correct-password", "session-ip")
			check(t, err)
			// Occupy both process-wide slots, as concurrent expensive checks would.
			for i := 0; i < 2; i++ {
				release, err := acquirePasswordCheck(context.Background())
				check(t, err)
				t.Cleanup(release)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := a.Login(ctx, username, "wrong", "queued-ip"); done <- err }()
			select {
			case err := <-done:
				t.Fatalf("login bypassed global capacity: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			validated := make(chan error, 1)
			go func() { _, err := a.Validate(context.Background(), id); validated <- err }()
			select {
			case err := <-validated:
				check(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("password queue blocked session validation")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("queued login ignored cancellation")
			}
		})
	}
	// All slots must be returned even after canceled requests.
	a, _ := testAuth(t)
	_, err := a.Login(context.Background(), "admin", "correct-password", "ip")
	check(t, err)
}

func TestAccountWaitCanCancelWithoutLeakingLocks(t *testing.T) {
	a, _ := testAuth(t)
	unlock, err := a.lockAccount(context.Background(), "admin")
	check(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.Login(ctx, "admin", "correct-password", "ip"); done <- err }()
	// Wait until Login is actually queued on this account, rather than merely
	// canceling before its goroutine gets scheduled.
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
waiting:
	for {
		a.mu.Lock()
		refs := a.verifying["admin"].refs
		a.mu.Unlock()
		if refs == 2 {
			break waiting
		}
		select {
		case <-tick.C:
		case <-deadline:
			cancel()
			unlock()
			t.Fatal("login did not queue on the account lock")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("account wait ignored cancellation")
	}
	unlock()
	a.mu.Lock()
	n := len(a.verifying)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("leaked account locks: %d", n)
	}
	_, err = a.Login(context.Background(), "admin", "correct-password", "ip")
	check(t, err)
}
