package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Errors from the session and nonce repository.
var (
	ErrNonceInvalid   = errors.New("nonce unknown, expired, or used")
	ErrSessionRevoked = errors.New("session revoked")
)

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// User is a row of users.
type User struct {
	ID             string
	Provider       string
	Subject        string
	Email          string
	EmailIsPrivate bool
	DisplayName    string
	Locale         string
	State          string
	CreatedAt      time.Time
}

// SignInIdentity is what a verified identity token gives.
type SignInIdentity struct {
	Provider       string // "apple" or "google"
	Subject        string
	Email          string
	EmailIsPrivate bool
	DisplayName    string // only stored if the user has no name yet
}

// CreateNonce stores the hash of a sign-in nonce.
func (s *Store) CreateNonce(ctx context.Context, hash string, expiresAt time.Time) error {
	_, err := s.w.ExecContext(ctx, "INSERT INTO auth_nonces (nonce_hash, expires_at) VALUES (?, ?)", hash, formatTime(expiresAt))
	return err
}

// ConsumeNonce marks a nonce used. It returns ErrNonceInvalid if the nonce is
// unknown, expired, or already used. It also deletes expired nonces.
func (s *Store) ConsumeNonce(ctx context.Context, hash string, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		n := formatTime(now)
		res, err := tx.ExecContext(ctx,
			"UPDATE auth_nonces SET used_at = ? WHERE nonce_hash = ? AND used_at IS NULL AND expires_at > ?", n, hash, n)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c != 1 {
			return ErrNonceInvalid
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM auth_nonces WHERE expires_at < ?", formatTime(now.Add(-time.Hour)))
		return err
	})
}

const userColumns = "id, provider, provider_subject, COALESCE(email, ''), email_is_private, COALESCE(display_name, ''), locale, state, created_at"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	err := row.Scan(&u.ID, &u.Provider, &u.Subject, &u.Email, &u.EmailIsPrivate, &u.DisplayName, &u.Locale, &u.State, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.CreatedAt = parseTime(created)
	return u, nil
}

// UpsertUser finds the user of a sign-in identity or creates one (ADR 0004:
// one user per sign-in identity, no merge by email). It updates the email and
// fills the display name if it is empty.
func (s *Store) UpsertUser(ctx context.Context, id SignInIdentity, locale string, now time.Time) (u User, created bool, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		u, err = scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE provider = ? AND provider_subject = ?", id.Provider, id.Subject))
		switch {
		case errors.Is(err, ErrNotFound):
			created = true
			_, err = tx.ExecContext(ctx,
				`INSERT INTO users (id, provider, provider_subject, email, email_is_private, display_name, locale, created_at)
				 VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?)`,
				newID(), id.Provider, id.Subject, id.Email, id.EmailIsPrivate, id.DisplayName, locale, formatTime(now))
		case err != nil:
			return err
		case u.State != "active":
			return ErrNotFound
		default:
			_, err = tx.ExecContext(ctx,
				`UPDATE users SET email = COALESCE(NULLIF(?, ''), email), email_is_private = ?,
				 display_name = COALESCE(display_name, NULLIF(?, '')), locale = ? WHERE id = ?`,
				id.Email, id.EmailIsPrivate, id.DisplayName, locale, u.ID)
		}
		if err != nil {
			return err
		}
		u, err = scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE provider = ? AND provider_subject = ?", id.Provider, id.Subject))
		return err
	})
	return u, created, err
}

// GetUser returns an active user.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(s.r.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ? AND state = 'active'", id))
}

// SetUserLocale stores the locale of the last request.
func (s *Store) SetUserLocale(ctx context.Context, userID, locale string) error {
	_, err := s.w.ExecContext(ctx, "UPDATE users SET locale = ? WHERE id = ? AND locale <> ?", locale, userID, locale)
	return err
}

// SetAppleRefreshToken stores the encrypted Apple refresh token of a user.
func (s *Store) SetAppleRefreshToken(ctx context.Context, userID string, enc []byte, now time.Time) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO apple_credentials (user_id, refresh_token_enc, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET refresh_token_enc = excluded.refresh_token_enc, updated_at = excluded.updated_at`,
		userID, enc, formatTime(now))
	return err
}

// AppleRefreshToken returns the encrypted Apple refresh token, or ErrNotFound.
func (s *Store) AppleRefreshToken(ctx context.Context, userID string) ([]byte, error) {
	var enc []byte
	err := s.r.QueryRowContext(ctx, "SELECT refresh_token_enc FROM apple_credentials WHERE user_id = ?", userID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return enc, err
}
