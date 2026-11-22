package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wanggang316/health-assistant/server/internal/auth/authtest"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

const (
	testBundleID = "com.gumpw.health-assistant"
	testClientID = "test.apps.googleusercontent.com"
)

type fakeApple struct {
	exchanged []string
	revoked   []string
	err       error
}

func (f *fakeApple) ExchangeCode(_ context.Context, code string) (string, error) {
	f.exchanged = append(f.exchanged, code)
	return "apple-rt-" + code, f.err
}

func (f *fakeApple) Revoke(_ context.Context, rt string) error {
	f.revoked = append(f.revoked, rt)
	return nil
}

type fixture struct {
	svc   *Service
	jwks  *authtest.JWKS
	apple *fakeApple
	store *store.Store
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{jwks: authtest.NewJWKS(t), apple: &fakeApple{}, store: st, now: time.Now().UTC()}
	clock := func() time.Time { return f.now }
	av := NewAppleVerifier(ctx, f.jwks.URL, testBundleID)
	gv := NewGoogleVerifier(ctx, f.jwks.URL, testClientID)
	av.now, gv.now = clock, clock
	f.svc = &Service{
		Store: st, Apple: av, Google: gv, AppleAPI: f.apple, EncKey: make([]byte, 32),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock,
	}
	return f
}

func (f *fixture) nonce(t *testing.T) string {
	t.Helper()
	n, _, err := f.svc.NewNonce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) appleClaims(nonce string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": AppleIssuer, "aud": testBundleID, "sub": "apple-sub-1",
		"iat": f.now.Unix(), "exp": f.now.Add(10 * time.Minute).Unix(),
		"nonce": HashNonceHex(nonce), "email": "x@privaterelay.appleid.com", "is_private_email": "true",
	}
}

func TestSignInAppleCreatesUserAndSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	n := f.nonce(t)
	res, err := f.svc.SignInApple(ctx, SignInRequest{
		IdentityToken: f.jwks.Sign(t, f.appleClaims(n), authtest.KID), Nonce: n,
		AuthorizationCode: "code1", FullName: "Gang Wang", Locale: "zh-Hans",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.User.Provider != "apple" || res.User.DisplayName != "Gang Wang" || !res.User.EmailIsPrivate || res.User.Locale != "zh-Hans" {
		t.Fatalf("user = %+v, created %v", res.User, res.Created)
	}
	p, err := f.svc.Authenticate(ctx, res.Tokens.AccessToken)
	if err != nil || p.UserID != res.User.ID {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}
	enc, err := f.store.AppleRefreshToken(ctx, res.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := f.svc.decrypt(enc)
	if err != nil || string(plain) != "apple-rt-code1" {
		t.Fatalf("stored apple refresh token = %q, %v", plain, err)
	}

	// Second sign-in: same user, name is not replaced.
	n2 := f.nonce(t)
	res2, err := f.svc.SignInApple(ctx, SignInRequest{IdentityToken: f.jwks.Sign(t, f.appleClaims(n2), authtest.KID), Nonce: n2, FullName: "Other", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created || res2.User.ID != res.User.ID || res2.User.DisplayName != "Gang Wang" {
		t.Fatalf("second sign-in user = %+v, created %v", res2.User, res2.Created)
	}
}

func TestSignInAppleSucceedsWhenCodeExchangeFails(t *testing.T) {
	f := newFixture(t)
	f.apple.err = errors.New("apple down")
	n := f.nonce(t)
	res, err := f.svc.SignInApple(context.Background(), SignInRequest{IdentityToken: f.jwks.Sign(t, f.appleClaims(n), authtest.KID), Nonce: n, AuthorizationCode: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AppleRefreshToken(context.Background(), res.User.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("apple refresh token: %v", err)
	}
}

func TestSignInRejectsBadTokens(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tests := []struct {
		name   string
		edit   func(c jwt.MapClaims, nonce string) (jwt.MapClaims, string, string) // claims, kid, nonce sent
		reason string
	}{
		{"wrong audience", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) {
			c["aud"] = "other"
			return c, authtest.KID, n
		}, "audience"},
		{"wrong issuer", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) {
			c["iss"] = "https://evil"
			return c, authtest.KID, n
		}, "issuer"},
		{"expired", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) {
			c["exp"] = f.now.Add(-2 * time.Minute).Unix()
			return c, authtest.KID, n
		}, "expired"},
		{"too old", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) {
			c["iat"] = f.now.Add(-11 * time.Minute).Unix()
			return c, authtest.KID, n
		}, "too_old"},
		{"unknown kid", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) { return c, "nope", n }, "signature"},
		{"nonce claim mismatch", func(c jwt.MapClaims, n string) (jwt.MapClaims, string, string) {
			c["nonce"] = HashNonceHex("x")
			return c, authtest.KID, n
		}, "nonce"},
		{"nonce not issued", func(c jwt.MapClaims, _ string) (jwt.MapClaims, string, string) {
			c["nonce"] = HashNonceHex("forged")
			return c, authtest.KID, "forged"
		}, "nonce"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := f.nonce(t)
			claims, kid, sent := tt.edit(f.appleClaims(n), n)
			_, err := f.svc.SignInApple(ctx, SignInRequest{IdentityToken: f.jwks.Sign(t, claims, kid), Nonce: sent})
			var ie *IdentityError
			if !errors.As(err, &ie) || ie.Reason != tt.reason {
				t.Fatalf("err = %v, want reason %q", err, tt.reason)
			}
		})
	}
}

func TestNonceIsSingleUseAndExpires(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	n := f.nonce(t)
	tok := f.jwks.Sign(t, f.appleClaims(n), authtest.KID)
	if _, err := f.svc.SignInApple(ctx, SignInRequest{IdentityToken: tok, Nonce: n}); err != nil {
		t.Fatal(err)
	}
	var ie *IdentityError
	if _, err := f.svc.SignInApple(ctx, SignInRequest{IdentityToken: tok, Nonce: n}); !errors.As(err, &ie) {
		t.Fatalf("replay: err = %v", err)
	}

	n2 := f.nonce(t)
	f.now = f.now.Add(NonceTTL + time.Second)
	c := f.appleClaims(n2)
	if _, err := f.svc.SignInApple(ctx, SignInRequest{IdentityToken: f.jwks.Sign(t, c, authtest.KID), Nonce: n2}); !errors.As(err, &ie) {
		t.Fatalf("expired nonce: err = %v", err)
	}
}

func TestSignInGoogleAcceptsBothIssuers(t *testing.T) {
	f := newFixture(t)
	for i, iss := range GoogleIssuers {
		n := f.nonce(t)
		tok := f.jwks.Sign(t, jwt.MapClaims{
			"iss": iss, "aud": testClientID, "sub": "g-sub", "exp": f.now.Add(time.Hour).Unix(),
			"nonce": HashNonceHex(n), "email": "g@example.com", "name": "Gump",
		}, authtest.KID)
		res, err := f.svc.SignInGoogle(context.Background(), SignInRequest{IdentityToken: tok, Nonce: n, Locale: "en"})
		if err != nil {
			t.Fatalf("iss %s: %v", iss, err)
		}
		if res.Created != (i == 0) || res.User.Provider != "google" || res.User.DisplayName != "Gump" {
			t.Fatalf("iss %s: %+v created %v", iss, res.User, res.Created)
		}
	}
}

func signInForTest(t *testing.T, f *fixture) SignInResult {
	t.Helper()
	n := f.nonce(t)
	res, err := f.svc.SignInApple(context.Background(), SignInRequest{IdentityToken: f.jwks.Sign(t, f.appleClaims(n), authtest.KID), Nonce: n})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := signInForTest(t, f)

	second, err := f.svc.Refresh(ctx, first.Tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	// Lost response: the old token works again within the grace period.
	f.now = f.now.Add(10 * time.Second)
	if _, err := f.svc.Refresh(ctx, first.Tokens.RefreshToken); err != nil {
		t.Fatalf("reuse within grace: %v", err)
	}
	// After the grace period, reuse revokes the whole family.
	f.now = f.now.Add(RotationGrace)
	if _, err := f.svc.Refresh(ctx, first.Tokens.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("reuse after grace: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, second.AccessToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("family member after reuse: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, second.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("family refresh after reuse: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, "ha_rt_unknown"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("unknown refresh token: %v", err)
	}
}

func TestAccessTokenExpiryAndLogout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := signInForTest(t, f)
	if _, err := f.svc.Authenticate(ctx, "ha_at_nope"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown: %v", err)
	}
	p, err := f.svc.Authenticate(ctx, res.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Logout(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, res.Tokens.AccessToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("after logout: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, res.Tokens.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("refresh after logout: %v", err)
	}

	res2 := signInForTest(t, f)
	f.now = f.now.Add(AccessTTL)
	if _, err := f.svc.Authenticate(ctx, res2.Tokens.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired access: %v", err)
	}
}

func TestAppleClientSecretAndRequests(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	var got []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = append(got, r.PostForm)
		if r.URL.Path == "/auth/token" {
			w.Write([]byte(`{"refresh_token":"rt1","access_token":"a"}`))
		}
	}))
	defer srv.Close()

	c, err := NewAppleClient("HC438T2B8P", testBundleID, "X9UM3BZKR3", p8)
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	rt, err := c.ExchangeCode(context.Background(), "code1")
	if err != nil || rt != "rt1" {
		t.Fatalf("ExchangeCode = %q, %v", rt, err)
	}
	if err := c.Revoke(context.Background(), "rt1"); err != nil {
		t.Fatal(err)
	}
	if got[0].Get("grant_type") != "authorization_code" || got[0].Get("code") != "code1" ||
		got[1].Get("token") != "rt1" || got[1].Get("token_type_hint") != "refresh_token" {
		t.Fatalf("forms = %v", got)
	}

	var claims jwt.RegisteredClaims
	tok, err := jwt.ParseWithClaims(got[0].Get("client_secret"), &claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience(AppleIssuer), jwt.WithIssuer("HC438T2B8P"), jwt.WithSubject(testBundleID))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Header["kid"] != "X9UM3BZKR3" || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != time.Hour {
		t.Fatalf("client secret header %v claims %+v", tok.Header, claims)
	}
	if got[1].Get("client_secret") != got[0].Get("client_secret") {
		t.Fatal("client secret is not cached")
	}
}
