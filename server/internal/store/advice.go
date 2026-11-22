package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Advice is a cached health advice text (ADR 0012).
type Advice struct {
	Kind        string // meal_feedback | daily_summary
	UserID      string
	MealEntryID string
	EntryDate   string
	Locale      string
	FactsJSON   string
	FactsHash   string
	Text        string
	Model       string
	Stale       bool
	GeneratedAt time.Time
}

// GetAdvice returns the cached advice for (meal entry, locale) or (date,
// locale), or ErrNotFound.
func (s *Store) GetAdvice(ctx context.Context, userID, kind, mealEntryID, date, locale string) (Advice, error) {
	q := "SELECT text, model, facts_hash, stale, generated_at FROM advice WHERE user_id = ? AND kind = ? AND locale = ? AND "
	args := []any{userID, kind, locale}
	if kind == "meal_feedback" {
		q += "meal_entry_id = ?"
		args = append(args, mealEntryID)
	} else {
		q += "entry_date = ?"
		args = append(args, date)
	}
	a := Advice{Kind: kind, UserID: userID, MealEntryID: mealEntryID, EntryDate: date, Locale: locale}
	var gen string
	err := s.r.QueryRowContext(ctx, q, args...).Scan(&a.Text, &a.Model, &a.FactsHash, &a.Stale, &gen)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.GeneratedAt = parseTime(gen)
	return a, nil
}

// PutAdvice stores fresh advice, replacing the old row of the same key.
func (s *Store) PutAdvice(ctx context.Context, a Advice) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if a.Kind == "meal_feedback" {
			_, err = tx.ExecContext(ctx, "DELETE FROM advice WHERE kind = 'meal_feedback' AND meal_entry_id = ? AND locale = ?", a.MealEntryID, a.Locale)
		} else {
			_, err = tx.ExecContext(ctx, "DELETE FROM advice WHERE kind = 'daily_summary' AND user_id = ? AND entry_date = ? AND locale = ?", a.UserID, a.EntryDate, a.Locale)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO advice (id, user_id, kind, meal_entry_id, entry_date, locale, facts_json, facts_hash, text, model, stale, generated_at)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, 0, ?)`,
			newID(), a.UserID, a.Kind, a.MealEntryID, a.EntryDate, a.Locale, a.FactsJSON, a.FactsHash, a.Text, a.Model, formatTime(a.GeneratedAt))
		return err
	})
}
