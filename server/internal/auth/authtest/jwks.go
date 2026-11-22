// Package authtest serves a test JWKS and signs identity tokens with its key.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// KID is the key ID of the served key.
const KID = "k1"

// JWKS is a test JWKS server with one RSA key.
type JWKS struct {
	Key *rsa.PrivateKey
	URL string
}

// NewJWKS starts a JWKS server that stops when the test ends.
func NewJWKS(t testing.TB) *JWKS {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": KID, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	return &JWKS{Key: key, URL: srv.URL}
}

// Sign signs claims with RS256 and the given key ID.
func (j *JWKS) Sign(t testing.TB, claims jwt.MapClaims, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(j.Key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
