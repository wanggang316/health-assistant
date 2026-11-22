package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Profile is a row of profiles.
type Profile struct {
	Sex           string
	BirthYear     int
	HeightCm      float64
	WeightKg      float64
	ActivityLevel string
	Goal          string
	ProteinPct    int
	FatPct        int
	CarbPct       int
	UpdatedAt     time.Time
}

// GetProfile returns the profile of a user, or ErrNotFound.
func (s *Store) GetProfile(ctx context.Context, userID string) (Profile, error) {
	var p Profile
	var updated string
	err := s.r.QueryRowContext(ctx,
		`SELECT sex, birth_year, height_cm, weight_kg, activity_level, goal, protein_pct, fat_pct, carb_pct, updated_at
		 FROM profiles WHERE user_id = ?`, userID).
		Scan(&p.Sex, &p.BirthYear, &p.HeightCm, &p.WeightKg, &p.ActivityLevel, &p.Goal, &p.ProteinPct, &p.FatPct, &p.CarbPct, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.UpdatedAt = parseTime(updated)
	return p, nil
}

// PutProfile creates or replaces the profile. In the same transaction it marks
// all health advice of the user stale (ADR 0012), because the daily target
// changes.
func (s *Store) PutProfile(ctx context.Context, userID string, p Profile, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO profiles (user_id, sex, birth_year, height_cm, weight_kg, activity_level, goal, protein_pct, fat_pct, carb_pct, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (user_id) DO UPDATE SET sex = excluded.sex, birth_year = excluded.birth_year,
			   height_cm = excluded.height_cm, weight_kg = excluded.weight_kg, activity_level = excluded.activity_level,
			   goal = excluded.goal, protein_pct = excluded.protein_pct, fat_pct = excluded.fat_pct,
			   carb_pct = excluded.carb_pct, updated_at = excluded.updated_at`,
			userID, p.Sex, p.BirthYear, p.HeightCm, p.WeightKg, p.ActivityLevel, p.Goal, p.ProteinPct, p.FatPct, p.CarbPct, formatTime(now))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE advice SET stale = 1 WHERE user_id = ? AND stale = 0", userID)
		return err
	})
}

// RecognitionQuotaUsed returns the number of recognitions used on a quota date.
func (s *Store) RecognitionQuotaUsed(ctx context.Context, userID, quotaDate string) (int, error) {
	var used int
	err := s.r.QueryRowContext(ctx, "SELECT used FROM recognition_quota WHERE user_id = ? AND quota_date = ?", userID, quotaDate).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return used, err
}
