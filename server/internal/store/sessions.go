package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session is a row of sessions. Token values are never stored, only hashes.
type Session struct {
	ID               string
	UserID           string
	FamilyID         string
	AccessHash       string
	AccessExpiresAt  time.Time
	RefreshHash      string
	RefreshExpiresAt time.Time
	RotatedAt        *time.Time
	RevokedAt        *time.Time
}

// NewSession is the token data of a session to create.
type NewSession struct {
	AccessHash       string
	AccessExpiresAt  time.Time
	RefreshHash      string
	RefreshExpiresAt time.Time
}

func insertSession(ctx context.Context, tx *sql.Tx, userID, familyID string, n NewSession, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, family_id, access_token_hash, access_expires_at, refresh_token_hash, refresh_expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), userID, familyID, n.AccessHash, formatTime(n.AccessExpiresAt), n.RefreshHash, formatTime(n.RefreshExpiresAt), formatTime(now))
	return err
}

// CreateSession starts a new session family.
func (s *Store) CreateSession(ctx context.Context, userID string, n NewSession, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return insertSession(ctx, tx, userID, newID(), n, now) })
}

const sessionColumns = "id, user_id, family_id, access_token_hash, access_expires_at, refresh_token_hash, refresh_expires_at, rotated_at, revoked_at"

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var ss Session
	var aexp, rexp string
	var rot, rev sql.NullString
	err := row.Scan(&ss.ID, &ss.UserID, &ss.FamilyID, &ss.AccessHash, &aexp, &ss.RefreshHash, &rexp, &rot, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return ss, ErrNotFound
	}
	if err != nil {
		return ss, err
	}
	ss.AccessExpiresAt, ss.RefreshExpiresAt = parseTime(aexp), parseTime(rexp)
	if rot.Valid {
		t := parseTime(rot.String)
		ss.RotatedAt = &t
	}
	if rev.Valid {
		t := parseTime(rev.String)
		ss.RevokedAt = &t
	}
	return ss, nil
}

// SessionByAccessHash returns the session of an access token. The caller
// checks expiry and revocation.
func (s *Store) SessionByAccessHash(ctx context.Context, hash string) (Session, error) {
	return scanSession(s.r.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE access_token_hash = ?", hash))
}

// RotateSession exchanges a refresh token for a new session in the same
// family (§3.4).
//
//   - Unknown token: ErrNotFound.
//   - Revoked or expired: ErrSessionRevoked.
//   - Already rotated less than grace ago (lost response on a flaky network):
//     a new session is issued in the same family.
//   - Already rotated earlier: reuse is a theft signal; the family is revoked
//     and ErrSessionRevoked is returned.
func (s *Store) RotateSession(ctx context.Context, refreshHash string, n NewSession, now time.Time, grace time.Duration) (userID string, err error) {
	var revokeFamily string
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		old, err := scanSession(tx.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE refresh_token_hash = ?", refreshHash))
		if err != nil {
			return err
		}
		switch {
		case old.RevokedAt != nil || !now.Before(old.RefreshExpiresAt):
			return ErrSessionRevoked
		case old.RotatedAt != nil && now.Sub(*old.RotatedAt) > grace:
			revokeFamily = old.FamilyID
			return ErrSessionRevoked
		}
		if old.RotatedAt == nil {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET rotated_at = ? WHERE id = ?", formatTime(now), old.ID); err != nil {
				return err
			}
		}
		userID = old.UserID
		return insertSession(ctx, tx, old.UserID, old.FamilyID, n, now)
	})
	if revokeFamily != "" {
		if rerr := s.revokeFamily(ctx, revokeFamily, now); rerr != nil {
			return "", rerr
		}
	}
	return userID, err
}

func (s *Store) revokeFamily(ctx context.Context, familyID string, now time.Time) error {
	_, err := s.w.ExecContext(ctx, "UPDATE sessions SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL", formatTime(now), familyID)
	return err
}

// RevokeSessionFamily revokes the family of a session (logout).
func (s *Store) RevokeSessionFamily(ctx context.Context, sessionID string, now time.Time) error {
	_, err := s.w.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL AND family_id = (SELECT family_id FROM sessions WHERE id = ?)",
		formatTime(now), sessionID)
	return err
}

// DeleteExpiredSessions removes sessions whose refresh token expired before cutoff.
func (s *Store) DeleteExpiredSessions(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.w.ExecContext(ctx, "DELETE FROM sessions WHERE refresh_expires_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
