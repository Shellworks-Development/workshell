package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A small, strict JWT verifier for SSO: RS256 and ES256 only, keys from a
// provider's JWKS. Anything else (none, HS*, unknown kid) is rejected.

var ssoHTTP = &http.Client{Timeout: 10 * time.Second}

func fetchJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := ssoHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
}

type jwks struct {
	url     string
	mu      sync.Mutex
	keys    map[string]crypto.PublicKey
	fetched time.Time
}

func newJWKS(url string) *jwks { return &jwks{url: url} }

// key returns the key with this id, refetching the set when the id is unknown
// (providers rotate keys) at most once a minute, and every hour regardless.
func (j *jwks) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	k, found := j.keys[kid]
	stale := time.Since(j.fetched) > time.Hour
	if found && !stale {
		return k, nil
	}
	if !found && time.Since(j.fetched) < time.Minute && !stale {
		return nil, errors.New("unknown signing key")
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	j.fetched = time.Now()
	if err := fetchJSON(ctx, j.url, &set); err != nil {
		if found {
			return k, nil
		}
		return nil, err
	}
	keys := map[string]crypto.PublicKey{}
	for _, raw := range set.Keys {
		if raw.Use != "" && raw.Use != "sig" {
			continue
		}
		switch raw.Kty {
		case "RSA":
			n, err1 := b64int(raw.N)
			e, err2 := b64int(raw.E)
			if err1 != nil || err2 != nil || !e.IsInt64() || n.BitLen() < 2048 {
				continue
			}
			keys[raw.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			if raw.Crv != "P-256" {
				continue
			}
			x, err1 := b64int(raw.X)
			y, err2 := b64int(raw.Y)
			if err1 != nil || err2 != nil || !elliptic.P256().IsOnCurve(x, y) {
				continue
			}
			keys[raw.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		}
	}
	j.keys = keys
	if k, found = keys[kid]; !found {
		return nil, errors.New("unknown signing key")
	}
	return k, nil
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New("bad key")
	}
	return new(big.Int).SetBytes(b), nil
}

type jwtClaims map[string]any

func (c jwtClaims) str(k string) string {
	v, _ := c[k].(string)
	return v
}

func (c jwtClaims) num(k string) (float64, bool) {
	v, ok := c[k].(float64)
	return v, ok
}

func (c jwtClaims) hasAudience(aud string) bool {
	switch v := c["aud"].(type) {
	case string:
		return v == aud
	case []any:
		for _, a := range v {
			if a == aud {
				return true
			}
		}
	}
	return false
}

// verifyJWT checks the signature, issuer, audience and time claims.
func verifyJWT(ctx context.Context, token string, keys *jwks, issuer, audience string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16<<10 {
		return nil, errors.New("malformed token")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	key, err := keys.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch k := key.(type) {
	case *rsa.PublicKey:
		if header.Alg != "RS256" || rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return nil, errors.New("bad signature")
		}
	case *ecdsa.PublicKey:
		if header.Alg != "ES256" || len(sig) != 64 ||
			!ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return nil, errors.New("bad signature")
		}
	default:
		return nil, errors.New("unsupported key")
	}
	var c jwtClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, err
	}
	if c.str("iss") != issuer {
		return nil, errors.New("wrong issuer")
	}
	if !c.hasAudience(audience) {
		return nil, errors.New("wrong audience")
	}
	now := float64(time.Now().Unix())
	const skew = 60
	exp, ok := c.num("exp")
	if !ok || now > exp+skew {
		return nil, errors.New("token expired")
	}
	if nbf, ok := c.num("nbf"); ok && now < nbf-skew {
		return nil, errors.New("token not yet valid")
	}
	if iat, ok := c.num("iat"); ok && now < iat-skew {
		return nil, errors.New("token issued in the future")
	}
	return c, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return errors.New("malformed token")
	}
	return json.Unmarshal(b, v)
}
