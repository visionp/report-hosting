package handler

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

type AccessVerifier interface {
	Verify(context.Context, string) error
}
type KeySource interface {
	Key(context.Context, string) (*rsa.PublicKey, error)
}
type accessVerifier struct {
	team, aud string
	keys      KeySource
}

func NewAccessVerifier(team, aud string, keys KeySource) AccessVerifier {
	return &accessVerifier{team, aud, keys}
}
func (v *accessVerifier) Verify(ctx context.Context, raw string) error {
	if raw == "" || v.keys == nil {
		return errors.New("missing Access token")
	}
	_, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, errors.New("missing key ID")
		}
		return v.keys.Key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("https://"+v.team), jwt.WithAudience(v.aud), jwt.WithExpirationRequired())
	return err
}

const (
	keysTTL       = 5 * time.Minute
	keysMinReload = time.Minute      // minimum gap between refetches for an unknown kid
	keysBackoff   = 30 * time.Second // pause after a failed fetch
)

type RemoteKeys struct {
	Team string
	// CertsURL and HTTPClient override the defaults (used by tests).
	CertsURL   string
	HTTPClient *http.Client

	fetchMu sync.Mutex // serializes network fetches; never held while mu is wanted by readers
	mu      sync.Mutex // guards the fields below
	keys    map[string]*rsa.PublicKey
	expires time.Time
	fetched time.Time
	retry   time.Time // no fetch before this time after a failure
}

var errUnknownKey = errors.New("unknown Access key")

// lookup returns the cached key, or whether a fetch is allowed right now.
func (k *RemoteKeys) lookup(kid string, now time.Time) (key *rsa.PublicKey, fetch bool, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	fresh := now.Before(k.expires)
	if fresh {
		if key := k.keys[kid]; key != nil {
			return key, false, nil
		}
	}
	if now.Before(k.retry) {
		return nil, false, errors.New("Access keys unavailable")
	}
	// Expired cache, or a fresh one that lacks this kid (key rotation): reload,
	// but not more often than keysMinReload so bogus kids cannot force fetches.
	if !fresh || !now.Before(k.fetched.Add(keysMinReload)) {
		return nil, true, nil
	}
	return nil, false, errUnknownKey
}

func (k *RemoteKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if key, fetch, err := k.lookup(kid, time.Now()); key != nil || !fetch {
		return key, err
	}
	// The network call runs outside mu; fetchMu makes waiters re-check the
	// refreshed cache or backoff instead of each issuing their own request.
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()
	if key, fetch, err := k.lookup(kid, time.Now()); key != nil || !fetch {
		return key, err
	}
	keys, err := k.fetch(ctx)
	now := time.Now()
	k.mu.Lock()
	if err != nil {
		if ctx.Err() == nil {
			k.retry = now.Add(keysBackoff)
		}
		k.mu.Unlock()
		return nil, err
	}
	k.keys, k.fetched, k.expires, k.retry = keys, now, now.Add(keysTTL), time.Time{}
	k.mu.Unlock()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errUnknownKey
}

func (k *RemoteKeys) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	url := k.CertsURL
	if url == "" {
		url = "https://" + k.Team + "/cdn-cgi/access/certs"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := k.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("Access keys unavailable")
	}
	var doc struct {
		Keys []struct{ Kid, Kty, N, E string }
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range doc.Keys {
		if j.Kty != "RSA" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(j.N)
		e, e2 := base64.RawURLEncoding.DecodeString(j.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(e) > 4 {
			continue
		}
		ex := 0
		for _, b := range e {
			ex = ex*256 + int(b)
		}
		if ex < 3 {
			continue
		}
		keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ex}
	}
	return keys, nil
}
