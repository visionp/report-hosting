package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func jwk(kid string, k *rsa.PublicKey) map[string]string {
	return map[string]string{"kid": kid, "kty": "RSA",
		"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})}
}

func TestRemoteKeysRotationAndBackoff(t *testing.T) {
	a, _ := rsa.GenerateKey(rand.Reader, 2048)
	b, _ := rsa.GenerateKey(rand.Reader, 2048)
	var hits atomic.Int32
	var fail atomic.Bool
	var keys atomic.Value
	keys.Store([]map[string]string{jwk("a", &a.PublicKey)})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": keys.Load()})
	}))
	defer srv.Close()
	k := &RemoteKeys{CertsURL: srv.URL}
	ctx := context.Background()
	if _, err := k.Key(ctx, "a"); err != nil || hits.Load() != 1 {
		t.Fatalf("initial fetch: %v hits=%d", err, hits.Load())
	}
	// Unknown kid right after a fetch must not trigger another request.
	if _, err := k.Key(ctx, "b"); err == nil || hits.Load() != 1 {
		t.Fatalf("unknown kid refetched too soon: %v hits=%d", err, hits.Load())
	}
	// After the reload gap, a rotated-in kid is picked up without waiting for the TTL.
	keys.Store([]map[string]string{jwk("a", &a.PublicKey), jwk("b", &b.PublicKey)})
	k.mu.Lock()
	k.fetched = time.Now().Add(-2 * keysMinReload)
	k.mu.Unlock()
	if key, err := k.Key(ctx, "b"); err != nil || key == nil || hits.Load() != 2 {
		t.Fatalf("rotation not picked up: %v hits=%d", err, hits.Load())
	}
	// A failing endpoint is hit once, then backed off.
	fail.Store(true)
	k.mu.Lock()
	k.expires = time.Now().Add(-time.Second)
	k.mu.Unlock()
	for i := 0; i < 5; i++ {
		if _, err := k.Key(ctx, "a"); err == nil {
			t.Fatal("expected error")
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3 (one failed fetch, then backoff)", hits.Load())
	}
}
