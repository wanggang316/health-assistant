package httpapi

import (
	_ "embed"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/wanggang316/health-assistant/server/internal/auth"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

type userJSON struct {
	ID          string  `json:"id"`
	Provider    string  `json:"provider"`
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	HasProfile  *bool   `json:"has_profile,omitempty"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Provider: u.Provider, DisplayName: optional(u.DisplayName), Email: optional(u.Email)}
}

type tokensJSON struct {
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func toTokensJSON(t auth.Tokens) tokensJSON {
	return tokensJSON{t.AccessToken, t.AccessExpiresAt.UTC(), t.RefreshToken, t.RefreshExpiresAt.UTC()}
}

type signInResponse struct {
	tokensJSON
	User userJSON `json:"user"`
}

func (s *Server) authNonce(w http.ResponseWriter, r *http.Request) error {
	nonce, exp, err := s.auth.NewNonce(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"nonce": nonce, "expires_at": exp.UTC()})
	return nil
}

type fullName struct {
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
}

// String joins the name parts. Han names put the family name first with no
// space.
func (n *fullName) String() string {
	if n == nil {
		return ""
	}
	g, f := strings.TrimSpace(n.GivenName), strings.TrimSpace(n.FamilyName)
	if strings.IndexFunc(g+f, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		return f + g
	}
	return strings.TrimSpace(g + " " + f)
}

func (s *Server) authApple(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IdentityToken     string    `json:"identity_token"`
		AuthorizationCode string    `json:"authorization_code"`
		Nonce             string    `json:"nonce"`
		FullName          *fullName `json:"full_name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return err
	}
	if body.IdentityToken == "" {
		return errInvalid("identity_token", "identity_token is required.")
	}
	if body.Nonce == "" {
		return errInvalid("nonce", "nonce is required.")
	}
	res, err := s.auth.SignInApple(r.Context(), auth.SignInRequest{
		IdentityToken: body.IdentityToken, Nonce: body.Nonce, AuthorizationCode: body.AuthorizationCode,
		FullName: body.FullName.String(), Locale: locale(r),
	})
	return s.writeSignIn(w, r, res, err)
}

func (s *Server) authGoogle(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return err
	}
	if body.IDToken == "" {
		return errInvalid("id_token", "id_token is required.")
	}
	if body.Nonce == "" {
		return errInvalid("nonce", "nonce is required.")
	}
	res, err := s.auth.SignInGoogle(r.Context(), auth.SignInRequest{IdentityToken: body.IDToken, Nonce: body.Nonce, Locale: locale(r)})
	return s.writeSignIn(w, r, res, err)
}

func (s *Server) writeSignIn(w http.ResponseWriter, r *http.Request, res auth.SignInResult, err error) error {
	if err != nil {
		return mapAuthError(err)
	}
	_, perr := s.store.GetProfile(r.Context(), res.User.ID)
	if perr != nil && perr != store.ErrNotFound {
		return perr
	}
	hasProfile := perr == nil
	u := toUserJSON(res.User)
	u.HasProfile = &hasProfile
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, signInResponse{toTokensJSON(res.Tokens), u})
	return nil
}

func (s *Server) authRefresh(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return err
	}
	if body.RefreshToken == "" {
		return errInvalid("refresh_token", "refresh_token is required.")
	}
	t, err := s.auth.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		return mapAuthError(err)
	}
	writeJSON(w, http.StatusOK, toTokensJSON(t))
	return nil
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) error {
	if err := s.auth.Logout(r.Context(), principal(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) authDev(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Subject string `json:"subject"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return err
	}
	if body.Subject == "" {
		return errInvalid("subject", "subject is required.")
	}
	res, err := s.auth.SignInDev(r.Context(), body.Subject, locale(r))
	return s.writeSignIn(w, r, res, err)
}

//go:embed privacy.html
var privacyHTML []byte

func (s *Server) privacy(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, err := w.Write(privacyHTML)
	return err
}
