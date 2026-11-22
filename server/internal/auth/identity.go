// Package auth verifies Apple and Google identity tokens, issues server
// sessions, and calls the Sign in with Apple REST API.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Default JWKS endpoints.
const (
	AppleJWKSURL  = "https://appleid.apple.com/auth/keys"
	GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	AppleIssuer   = "https://appleid.apple.com"
)

// GoogleIssuers are the accepted "iss" values of Google ID tokens.
var GoogleIssuers = []string{"accounts.google.com", "https://accounts.google.com"}

// IdentityError is a failed identity token check. Reason goes to
// error.details.reason.
type IdentityError struct{ Reason string }

func (e *IdentityError) Error() string { return "identity token: " + e.Reason }

// Identity is the verified content of an identity token.
type Identity struct {
	Subject        string
	Email          string
	EmailIsPrivate bool
	Name           string
	NonceClaim     string
}

// lazyJWKS creates the JWKS client on first use, so that the server starts
// when the provider is not reachable.
type lazyJWKS struct {
	ctx context.Context
	url string
	mu  sync.Mutex
	kf  keyfunc.Keyfunc
}

func (l *lazyJWKS) get() (keyfunc.Keyfunc, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kf != nil {
		return l.kf, nil
	}
	kf, err := keyfunc.NewDefaultCtx(l.ctx, []string{l.url})
	if err != nil {
		return nil, fmt.Errorf("jwks %s: %w", l.url, err)
	}
	l.kf = kf
	return kf, nil
}

// Verifier checks identity tokens of one provider.
type Verifier struct {
	jwks     *lazyJWKS
	issuers  []string
	audience string
	maxAge   time.Duration // 0: no "iat" age check
	now      func() time.Time
}

// NewAppleVerifier checks Apple identity tokens (§3.2).
func NewAppleVerifier(ctx context.Context, jwksURL, bundleID string) *Verifier {
	return &Verifier{jwks: &lazyJWKS{ctx: ctx, url: jwksURL}, issuers: []string{AppleIssuer}, audience: bundleID, maxAge: 10 * time.Minute, now: time.Now}
}

// NewGoogleVerifier checks Google ID tokens (§3.3).
func NewGoogleVerifier(ctx context.Context, jwksURL, clientID string) *Verifier {
	return &Verifier{jwks: &lazyJWKS{ctx: ctx, url: jwksURL}, issuers: GoogleIssuers, audience: clientID, now: time.Now}
}

type identityClaims struct {
	jwt.RegisteredClaims
	Email          string `json:"email"`
	IsPrivateEmail any    `json:"is_private_email"` // Apple sends a bool or "true"
	Name           string `json:"name"`
	Nonce          string `json:"nonce"`
}

// Verify checks the signature (RS256), issuer, audience, expiry, and age of a
// token. It returns an *IdentityError for a bad token.
func (v *Verifier) Verify(ctx context.Context, token string) (Identity, error) {
	kf, err := v.jwks.get()
	if err != nil {
		return Identity{}, err
	}
	var c identityClaims
	_, err = jwt.ParseWithClaims(token, &c, kf.KeyfuncCtx(ctx),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60*time.Second),
		jwt.WithTimeFunc(v.now),
	)
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return Identity{}, &IdentityError{"expired"}
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return Identity{}, &IdentityError{"audience"}
	case err != nil:
		return Identity{}, &IdentityError{"signature"}
	}
	issOK := false
	for _, iss := range v.issuers {
		issOK = issOK || c.Issuer == iss
	}
	if !issOK {
		return Identity{}, &IdentityError{"issuer"}
	}
	if c.Subject == "" {
		return Identity{}, &IdentityError{"subject"}
	}
	if v.maxAge > 0 && (c.IssuedAt == nil || v.now().Sub(c.IssuedAt.Time) > v.maxAge) {
		return Identity{}, &IdentityError{"too_old"}
	}
	return Identity{
		Subject:        c.Subject,
		Email:          c.Email,
		EmailIsPrivate: truthy(c.IsPrivateEmail),
		Name:           c.Name,
		NonceClaim:     c.Nonce,
	}, nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	}
	return false
}

// HashNonceHex is the value the app passes to Apple or Google as the nonce:
// sha256 of the server nonce, hex encoded (§3.1).
func HashNonceHex(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
