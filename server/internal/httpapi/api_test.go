package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wanggang316/health-assistant/server/internal/auth"
	"github.com/wanggang316/health-assistant/server/internal/auth/authtest"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/nutrition/nutritiontest"
	"github.com/wanggang316/health-assistant/server/internal/recognition"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

const bundleID = "com.gumpw.health-assistant"

type recordingApple struct {
	mu         sync.Mutex
	revoked    []string
	failRevoke bool
}

func (*recordingApple) ExchangeCode(_ context.Context, code string) (string, error) {
	return "rt-" + code, nil
}
func (a *recordingApple) Revoke(_ context.Context, rt string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failRevoke {
		return errors.New("invalid_grant")
	}
	a.revoked = append(a.revoked, rt)
	return nil
}

type testAPI struct {
	t      *testing.T
	srv    *httptest.Server
	jwks   *authtest.JWKS
	now    time.Time
	gem    *fakeGemini
	photos *memPhotos
	apple  *recordingApple
	store  *store.Store
}

func newTestAPI(t *testing.T) *testAPI {
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
	a := &testAPI{t: t, jwks: authtest.NewJWKS(t), now: time.Now().UTC().Truncate(time.Second), gem: &fakeGemini{},
		photos: &memPhotos{objects: map[string][]byte{}}, apple: &recordingApple{}, store: st}
	clock := func() time.Time { return a.now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &auth.Service{
		Store: st, Apple: auth.NewAppleVerifier(ctx, a.jwks.URL, bundleID), Google: auth.NewGoogleVerifier(ctx, a.jwks.URL, "gid"),
		AppleAPI: a.apple, EncKey: make([]byte, 32), Log: log, Now: time.Now,
	}
	nut := nutritiontest.Open(t)
	rec := &recognition.Service{
		Store: st, Gemini: a.gem, Model: "gemini-test", Photos: a.photos,
		Matcher:    &nutrition.Matcher{DB: nut, Cache: st, Log: log},
		QuotaLimit: 30, GlobalLimit: 500, Log: log, Now: clock,
	}
	a.srv = httptest.NewServer(New(Deps{Store: st, Auth: svc, Nutrition: nut, Recognition: rec, Photos: a.photos,
		Log: log, Version: "abc123", RecognitionQuota: 30, Now: clock}))
	t.Cleanup(a.srv.Close)
	return a
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
	return m
}

func (a *testAPI) do(method, path, token string, body any, headers ...string) response {
	a.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, a.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, resp.Header, b}
}

func (a *testAPI) signInApple() (access, refresh string) {
	a.t.Helper()
	nonce := a.do("POST", "/v1/auth/nonce", "", nil).json(a.t)["nonce"].(string)
	now := time.Now()
	tok := a.jwks.Sign(a.t, jwt.MapClaims{
		"iss": auth.AppleIssuer, "aud": bundleID, "sub": "s1", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": auth.HashNonceHex(nonce),
	}, authtest.KID)
	r := a.do("POST", "/v1/auth/apple", "", map[string]any{
		"identity_token": tok, "authorization_code": "c", "nonce": nonce,
		"full_name": map[string]string{"given_name": "刚", "family_name": "王"},
	}, "Accept-Language", "zh-Hans")
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		a.t.Fatalf("sign in: %d %s", r.status, r.body)
	}
	assertShape(a.t, r.body, "auth_sign_in.json")
	m := r.json(a.t)
	return m["access_token"].(string), m["refresh_token"].(string)
}

// assertShape checks that body has the same keys and JSON types as the
// fixture in docs/api/fixtures. A null on either side matches any type.
func assertShape(t *testing.T, body []byte, fixture string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "fixtures", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("fixture %s: %v", fixture, err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body: %v", err)
	}
	if diff := shapeDiff("$", want, got); diff != "" {
		t.Fatalf("response does not match %s: %s\nbody: %s", fixture, diff, body)
	}
}

func shapeDiff(path string, want, got any) string {
	if want == nil || got == nil {
		return ""
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: want object, got %T", path, got)
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			wv, wok := w[k]
			gv, gok := g[k]
			if !wok || !gok {
				return fmt.Sprintf("%s.%s: in fixture %v, in response %v", path, k, wok, gok)
			}
			if d := shapeDiff(path+"."+k, wv, gv); d != "" {
				return d
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: want array, got %T", path, got)
		}
		if len(w) > 0 {
			for i, gv := range g {
				if d := shapeDiff(fmt.Sprintf("%s[%d]", path, i), w[0], gv); d != "" {
					return d
				}
			}
		}
	default:
		if fmt.Sprintf("%T", want) != fmt.Sprintf("%T", got) {
			return fmt.Sprintf("%s: want %T, got %T", path, want, got)
		}
	}
	return ""
}

// errorCode checks the error body (§5.1) and returns its code. details is a
// free-form object, so it is not compared with the fixture.
func errorCode(t *testing.T, r response) string {
	t.Helper()
	var b struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(r.body, &b); err != nil {
		t.Fatalf("error body %s: %v", r.body, err)
	}
	if b.Error.Code == "" || b.Error.Message == "" || b.RequestID != r.header.Get("X-Request-Id") ||
		(len(b.Error.Details) > 0 && b.Error.Details[0] != '{') {
		t.Fatalf("bad error body: %s", r.body)
	}
	return b.Error.Code
}

func TestHealthz(t *testing.T) {
	a := newTestAPI(t)
	r := a.do("GET", "/healthz", "", nil)
	if r.status != 200 || r.json(t)["version"] != "abc123" || r.header.Get("X-Request-Id") == "" {
		t.Fatalf("healthz: %d %s", r.status, r.body)
	}
}

func TestSignInProfileRefreshLogout(t *testing.T) {
	a := newTestAPI(t)
	assertShape(t, a.do("POST", "/v1/auth/nonce", "", nil).body, "auth_nonce.json")
	access, refresh := a.signInApple()

	a.now = time.Date(2026, 11, 22, 4, 12, 0, 0, time.UTC)
	r := a.do("GET", "/v1/me", access, nil, "X-Utc-Offset-Minutes", "480")
	if r.status != 200 {
		t.Fatalf("me: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "me_without_profile.json")
	me := r.json(t)
	if me["user"].(map[string]any)["display_name"] != "王刚" {
		t.Fatalf("display_name = %v", me["user"])
	}
	if q := me["recognition_quota"].(map[string]any); q["resets_at"] != "2026-11-22T16:00:00Z" || q["used"] != 0.0 {
		t.Fatalf("quota = %v", q)
	}

	profile := map[string]any{"sex": "male", "birth_year": 1995, "height_cm": 170, "weight_kg": 61, "activity_level": "light", "goal": "lose"}
	r = a.do("PUT", "/v1/me/profile", access, profile)
	if r.status != 200 {
		t.Fatalf("put profile: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "me.json")
	tgt := r.json(t)["daily_target"].(map[string]any)
	if tgt["kcal"] != 1779.0 || tgt["protein_g"] != 111.0 {
		t.Fatalf("daily_target = %v", tgt)
	}

	// Signing in again reports the profile.
	_, _ = a.signInApple()

	r = a.do("POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	if r.status != 200 {
		t.Fatalf("refresh: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "auth_tokens.json")
	access2 := r.json(t)["access_token"].(string)

	if r := a.do("POST", "/v1/auth/logout", access2, nil); r.status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", r.status, r.body)
	}
	if code := errorCode(t, a.do("GET", "/v1/me", access2, nil)); code != "session_revoked" {
		t.Fatalf("after logout: %s", code)
	}
	if code := errorCode(t, a.do("GET", "/v1/me", access, nil)); code != "session_revoked" {
		t.Fatalf("first token after logout: %s", code)
	}
}

func TestErrors(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	tests := []struct {
		name         string
		method, path string
		token        string
		body         any
		headers      []string
		status       int
		code         string
	}{
		{"no token", "GET", "/v1/me", "", nil, nil, 401, "unauthenticated"},
		{"unknown token", "GET", "/v1/me", "ha_at_x", nil, nil, 401, "unauthenticated"},
		{"bad json", "POST", "/v1/auth/google", "", "{", nil, 400, "invalid_request"},
		{"unknown field", "POST", "/v1/auth/google", "", `{"id_token":"x","nonce":"n","extra":1}`, nil, 400, "invalid_request"},
		{"missing nonce", "POST", "/v1/auth/google", "", map[string]string{"id_token": "x"}, nil, 400, "invalid_request"},
		{"bad identity token", "POST", "/v1/auth/google", "", map[string]string{"id_token": "x", "nonce": "n"}, nil, 401, "invalid_identity_token"},
		{"unknown refresh", "POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": "ha_rt_x"}, nil, 401, "session_revoked"},
		{"bad profile", "PUT", "/v1/me/profile", access, map[string]any{"sex": "x"}, nil, 400, "invalid_request"},
		{"bad macro split", "PUT", "/v1/me/profile", access, map[string]any{"sex": "female", "birth_year": 1990, "height_cm": 160, "weight_kg": 50, "activity_level": "light", "goal": "keep", "macro_split": map[string]int{"protein_pct": 50, "fat_pct": 30, "carb_pct": 30}}, nil, 400, "invalid_request"},
		{"bad offset", "GET", "/v1/me", access, nil, []string{"X-Utc-Offset-Minutes", "900"}, 400, "invalid_request"},
		{"body too large", "POST", "/v1/auth/refresh", "", `{"refresh_token":"` + strings.Repeat("a", 70<<10) + `"}`, nil, 413, "payload_too_large"},
		{"unknown route", "GET", "/v1/nope", "", nil, nil, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := a.do(tt.method, tt.path, tt.token, tt.body, tt.headers...)
			if r.status != tt.status {
				t.Fatalf("status = %d, want %d: %s", r.status, tt.status, r.body)
			}
			if code := errorCode(t, r); code != tt.code {
				t.Fatalf("code = %s, want %s", code, tt.code)
			}
		})
	}
}

func TestProfileUnder18HasNoTarget(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	r := a.do("PUT", "/v1/me/profile", access, map[string]any{"sex": "female", "birth_year": 2010, "height_cm": 160, "weight_kg": 50, "activity_level": "light", "goal": "keep"})
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	m := r.json(t)
	if m["daily_target"] != nil || m["profile"] == nil {
		t.Fatalf("me = %v", m)
	}
}

func TestFoodSearch(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	r := a.do("GET", "/v1/foods/search?q=%E7%95%AA%E8%8C%84&limit=5", access, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	assertShape(t, r.body, "foods_search.json")
	foods := r.json(t)["foods"].([]any)
	if len(foods) != 1 || foods[0].(map[string]any)["fdc_id"] != float64(nutritiontest.Tomato) {
		t.Fatalf("foods = %v", foods)
	}
	if code := errorCode(t, a.do("GET", "/v1/foods/search?q=", access, nil)); code != "invalid_request" {
		t.Fatalf("empty q: %s", code)
	}
}
