package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/store"
)

// Token lifetimes and limits (§3.1, §3.4).
const (
	NonceTTL      = 5 * time.Minute
	AccessTTL     = time.Hour
	RefreshTTL    = 60 * 24 * time.Hour
	RotationGrace = 30 * time.Second
	accessPrefix  = "ha_at_"
	refreshPrefix = "ha_rt_"
)

// Errors returned by Service.
var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrTokenExpired    = errors.New("token expired")
	ErrSessionRevoked  = errors.New("session revoked")
)

// AppleAPI is the part of AppleClient that Service uses.
type AppleAPI interface {
	ExchangeCode(ctx context.Context, code string) (string, error)
	Revoke(ctx context.Context, refreshToken string) error
}

// Service issues sessions after a verified sign-in.
type Service struct {
	Store    *store.Store
	Apple    *Verifier
	Google   *Verifier
	AppleAPI AppleAPI
	EncKey   []byte // 32 bytes, AES-256-GCM for Apple refresh tokens
	Log      *slog.Logger
	Now      func() time.Time
}

// Tokens is a new access and refresh token pair.
type Tokens struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID    string
	SessionID string
}

func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *Service) newTokens(now time.Time) (Tokens, store.NewSession) {
	t := Tokens{
		AccessToken:      randomToken(accessPrefix),
		AccessExpiresAt:  now.Add(AccessTTL),
		RefreshToken:     randomToken(refreshPrefix),
		RefreshExpiresAt: now.Add(RefreshTTL),
	}
	return t, store.NewSession{
		AccessHash: hashToken(t.AccessToken), AccessExpiresAt: t.AccessExpiresAt,
		RefreshHash: hashToken(t.RefreshToken), RefreshExpiresAt: t.RefreshExpiresAt,
	}
}

// NewNonce makes and stores a single-use sign-in nonce.
func (s *Service) NewNonce(ctx context.Context) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	exp := s.Now().Add(NonceTTL)
	return nonce, exp, s.Store.CreateNonce(ctx, hashToken(nonce), exp)
}

// SignInRequest is the input of a sign-in.
type SignInRequest struct {
	IdentityToken     string
	Nonce             string
	AuthorizationCode string // Apple only
	FullName          string // Apple only, first authorization
	Locale            string
}

// SignInResult is the result of a sign-in.
type SignInResult struct {
	Tokens  Tokens
	User    store.User
	Created bool
}

// SignInApple verifies an Apple identity token and starts a session.
func (s *Service) SignInApple(ctx context.Context, r SignInRequest) (SignInResult, error) {
	res, err := s.signIn(ctx, "apple", s.Apple, r)
	if err != nil || r.AuthorizationCode == "" {
		return res, err
	}
	// The exchange is best effort: sign-in succeeds without it, and account
	// deletion asks for a fresh code (§3.2).
	if err := s.storeAppleRefreshToken(ctx, res.User.ID, r.AuthorizationCode); err != nil {
		s.Log.Warn("apple code exchange failed", "user_id", res.User.ID, "err", err)
	}
	return res, nil
}

// SignInGoogle verifies a Google ID token and starts a session.
func (s *Service) SignInGoogle(ctx context.Context, r SignInRequest) (SignInResult, error) {
	return s.signIn(ctx, "google", s.Google, r)
}

func (s *Service) signIn(ctx context.Context, provider string, v *Verifier, r SignInRequest) (SignInResult, error) {
	id, err := v.Verify(ctx, r.IdentityToken)
	if err != nil {
		return SignInResult{}, err
	}
	if r.Nonce == "" || id.NonceClaim != HashNonceHex(r.Nonce) {
		return SignInResult{}, &IdentityError{"nonce"}
	}
	now := s.Now()
	if err := s.Store.ConsumeNonce(ctx, hashToken(r.Nonce), now); err != nil {
		if errors.Is(err, store.ErrNonceInvalid) {
			return SignInResult{}, &IdentityError{"nonce"}
		}
		return SignInResult{}, err
	}
	name := strings.TrimSpace(r.FullName)
	if name == "" {
		name = id.Name
	}
	u, created, err := s.Store.UpsertUser(ctx, store.SignInIdentity{
		Provider: provider, Subject: id.Subject, Email: id.Email, EmailIsPrivate: id.EmailIsPrivate, DisplayName: name,
	}, r.Locale, now)
	if err != nil {
		return SignInResult{}, err
	}
	t, ns := s.newTokens(now)
	if err := s.Store.CreateSession(ctx, u.ID, ns, now); err != nil {
		return SignInResult{}, err
	}
	return SignInResult{Tokens: t, User: u, Created: created}, nil
}

func (s *Service) storeAppleRefreshToken(ctx context.Context, userID, code string) error {
	rt, err := s.AppleAPI.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}
	enc, err := s.encrypt([]byte(rt))
	if err != nil {
		return err
	}
	return s.Store.SetAppleRefreshToken(ctx, userID, enc, s.Now())
}

// Refresh rotates a refresh token (§3.4).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	now := s.Now()
	t, ns := s.newTokens(now)
	_, err := s.Store.RotateSession(ctx, hashToken(refreshToken), ns, now, RotationGrace)
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrSessionRevoked):
		return Tokens{}, ErrSessionRevoked
	case err != nil:
		return Tokens{}, err
	}
	return t, nil
}

// Authenticate checks an access token.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	if !strings.HasPrefix(accessToken, accessPrefix) {
		return Principal{}, ErrUnauthenticated
	}
	ss, err := s.Store.SessionByAccessHash(ctx, hashToken(accessToken))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Principal{}, ErrUnauthenticated
	case err != nil:
		return Principal{}, err
	case ss.RevokedAt != nil:
		return Principal{}, ErrSessionRevoked
	case !s.Now().Before(ss.AccessExpiresAt):
		return Principal{}, ErrTokenExpired
	}
	return Principal{UserID: ss.UserID, SessionID: ss.ID}, nil
}

// Logout revokes the session family of the caller.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	return s.Store.RevokeSessionFamily(ctx, p.SessionID, s.Now())
}

func (s *Service) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.EncKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encrypt returns nonce || ciphertext.
func (s *Service) encrypt(plain []byte) ([]byte, error) {
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

func (s *Service) decrypt(enc []byte) ([]byte, error) {
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(enc) < g.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return g.Open(nil, enc[:g.NonceSize()], enc[g.NonceSize():], nil)
}

// ErrAppleReauthRequired means account deletion needs a fresh Apple
// authorization code (§3.6).
var ErrAppleReauthRequired = errors.New("apple reauthorization required")

// RevokeApple revokes the Apple tokens of a user before account deletion.
// It uses a fresh authorization code if given, else the stored refresh token.
func (s *Service) RevokeApple(ctx context.Context, userID, code string) error {
	var rt string
	if code != "" {
		t, err := s.AppleAPI.ExchangeCode(ctx, code)
		if err != nil {
			s.Log.Warn("apple code exchange for deletion failed", "user_id", userID, "err", err)
		} else {
			rt = t
		}
	}
	if rt == "" {
		enc, err := s.Store.AppleRefreshToken(ctx, userID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrAppleReauthRequired
		}
		if err != nil {
			return err
		}
		plain, err := s.decrypt(enc)
		if err != nil {
			return err
		}
		rt = string(plain)
	}
	if err := s.AppleAPI.Revoke(ctx, rt); err != nil {
		// A stored token can be stale; ask for a new code once.
		if code == "" {
			return ErrAppleReauthRequired
		}
		return err
	}
	return nil
}
