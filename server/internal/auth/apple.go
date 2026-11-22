package auth

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AppleRESTURL is the base URL of the Sign in with Apple REST API.
const AppleRESTURL = "https://appleid.apple.com"

// AppleClient calls /auth/token and /auth/revoke with an ES256 client secret
// (§3.2, §3.6).
type AppleClient struct {
	BaseURL  string
	TeamID   string
	ClientID string // bundle ID
	KeyID    string
	Key      *ecdsa.PrivateKey
	HTTP     *http.Client
	Now      func() time.Time

	mu        sync.Mutex
	secret    string
	secretExp time.Time
}

// NewAppleClient parses the .p8 key (PEM, PKCS#8).
func NewAppleClient(teamID, clientID, keyID string, p8PEM []byte) (*AppleClient, error) {
	key, err := jwt.ParseECPrivateKeyFromPEM(p8PEM)
	if err != nil {
		return nil, fmt.Errorf("apple key: %w", err)
	}
	return &AppleClient{
		BaseURL: AppleRESTURL, TeamID: teamID, ClientID: clientID, KeyID: keyID, Key: key,
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}, nil
}

// clientSecret returns a cached ES256 JWT that is valid for one hour.
func (c *AppleClient) clientSecret() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	if c.secret != "" && now.Before(c.secretExp.Add(-5*time.Minute)) {
		return c.secret, nil
	}
	exp := now.Add(time.Hour)
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    c.TeamID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
		Audience:  jwt.ClaimStrings{AppleIssuer},
		Subject:   c.ClientID,
	})
	tok.Header["kid"] = c.KeyID
	s, err := tok.SignedString(c.Key)
	if err != nil {
		return "", err
	}
	c.secret, c.secretExp = s, exp
	return s, nil
}

func (c *AppleClient) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	secret, err := c.clientSecret()
	if err != nil {
		return nil, err
	}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, fmt.Errorf("apple %s: status %d %s", path, resp.StatusCode, e.Error)
	}
	return body, nil
}

// ExchangeCode exchanges an authorization code for an Apple refresh token.
func (c *AppleClient) ExchangeCode(ctx context.Context, code string) (string, error) {
	body, err := c.post(ctx, "/auth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}})
	if err != nil {
		return "", err
	}
	var r struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.RefreshToken == "" {
		return "", errors.New("apple /auth/token: no refresh_token")
	}
	return r.RefreshToken, nil
}

// Revoke revokes an Apple refresh token. Apple returns 200 also for a token
// that is already invalid.
func (c *AppleClient) Revoke(ctx context.Context, refreshToken string) error {
	_, err := c.post(ctx, "/auth/revoke", url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}})
	return err
}
