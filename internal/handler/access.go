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

type RemoteKeys struct {
	Team    string
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
}

func (k *RemoteKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if time.Now().Before(k.expires) {
		if key := k.keys[kid]; key != nil {
			return key, nil
		}
		return nil, errors.New("unknown Access key")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://"+k.Team+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
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
	k.keys = keys
	k.expires = time.Now().Add(5 * time.Minute)
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown Access key")
}
