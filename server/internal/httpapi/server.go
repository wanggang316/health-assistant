// Package httpapi is the REST API: router, middleware, handlers, and error
// mapping (§5).
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wanggang316/health-assistant/server/internal/auth"
	"github.com/wanggang316/health-assistant/server/internal/localtime"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/photo"
	"github.com/wanggang316/health-assistant/server/internal/recognition"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

// Server holds the handler dependencies.
type Server struct {
	store            *store.Store
	auth             *auth.Service
	nutrition        *nutrition.DB
	recognition      *recognition.Service
	photos           photo.Storage
	log              *slog.Logger
	version          string
	recognitionQuota int
	now              func() time.Time
}

// Deps are the dependencies of New.
type Deps struct {
	Store            *store.Store
	Auth             *auth.Service
	Nutrition        *nutrition.DB
	Recognition      *recognition.Service
	Photos           photo.Storage
	Log              *slog.Logger
	Version          string
	RecognitionQuota int
	Now              func() time.Time
}

// New returns the API handler.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Server{store: d.Store, auth: d.Auth, nutrition: d.Nutrition, recognition: d.Recognition, photos: d.Photos, log: d.Log, version: d.Version, recognitionQuota: d.RecognitionQuota, now: d.Now}
	mux := http.NewServeMux()
	handle := func(pattern string, h func(http.ResponseWriter, *http.Request) error) {
		mux.Handle(pattern, s.wrap(h))
	}
	authed := func(pattern string, h func(http.ResponseWriter, *http.Request) error) {
		handle(pattern, s.requireAuth(h))
	}

	handle("GET /healthz", s.healthz)
	handle("POST /v1/auth/nonce", s.authNonce)
	handle("POST /v1/auth/apple", s.authApple)
	handle("POST /v1/auth/google", s.authGoogle)
	handle("POST /v1/auth/refresh", s.authRefresh)
	authed("POST /v1/auth/logout", s.authLogout)
	authed("GET /v1/me", s.getMe)
	authed("PUT /v1/me/profile", s.putProfile)
	authed("DELETE /v1/me", s.deleteMe)
	authed("GET /v1/foods/search", s.searchFoods)
	authed("POST /v1/recognitions", s.createRecognition)
	authed("GET /v1/recognitions/{id}", s.getRecognition)
	handle("/", func(http.ResponseWriter, *http.Request) error { return errNotFound })
	return s.accessLog(mux)
}

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
	ctxLogUser // *string filled by requireAuth for the access log
)

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(ctxPrincipal).(auth.Principal)
	return p
}

// statusRecorder keeps the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	userID string
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// accessLog sets X-Request-Id and writes one log line per request with the
// route pattern, not the raw path (§10.8).
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := uuid.Must(uuid.NewV7()).String()
		w.Header().Set("X-Request-Id", id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		r = r.WithContext(context.WithValue(ctx, ctxLogUser, &rec.userID))
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"request_id", id, "method", r.Method, "route", r.Pattern, "status", rec.status,
			"latency_ms", time.Since(start).Milliseconds(), "user_id", rec.userID)
	})
}

func (s *Server) wrap(h func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// requireAuth checks the bearer access token and stores the principal.
func (s *Server) requireAuth(h func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			return errUnauthenticated
		}
		p, err := s.auth.Authenticate(r.Context(), strings.TrimSpace(token))
		if err != nil {
			return mapAuthError(err)
		}
		if slot, ok := r.Context().Value(ctxLogUser).(*string); ok {
			*slot = p.UserID
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p))
		if err := s.store.SetUserLocale(r.Context(), p.UserID, locale(r)); err != nil {
			return err
		}
		return h(w, r)
	}
}

func mapAuthError(err error) error {
	var ie *auth.IdentityError
	switch {
	case errors.As(err, &ie):
		return &apiError{Status: http.StatusUnauthorized, Code: "invalid_identity_token", Message: "Identity token check failed.", Details: map[string]any{"reason": ie.Reason}}
	case errors.Is(err, auth.ErrTokenExpired):
		return errTokenExpired
	case errors.Is(err, auth.ErrSessionRevoked):
		return errSessionRevoked
	case errors.Is(err, auth.ErrUnauthenticated):
		return errUnauthenticated
	}
	return err
}

// locale maps Accept-Language to a supported locale.
func locale(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "zh") {
		return "zh-Hans"
	}
	return "en"
}

// utcOffset reads the optional X-Utc-Offset-Minutes header (default 0). It
// sets the local day for the recognition quota in GET /v1/me.
func utcOffset(r *http.Request) (int, error) {
	v := r.Header.Get("X-Utc-Offset-Minutes")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || !localtime.ValidOffset(n) {
		return 0, errInvalid("X-Utc-Offset-Minutes", "UTC offset must be an integer from -720 to 840.")
	}
	return n, nil
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.Ping(r.Context()); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
	return nil
}
