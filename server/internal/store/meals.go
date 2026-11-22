package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Errors of the meal entry repository.
var (
	ErrAlreadyConfirmed = errors.New("recognition already has a meal entry")
	ErrBadCursor        = errors.New("bad cursor")
)

// IngredientRow is a stored ingredient with its nutrient snapshot.
type IngredientRow struct {
	ID               string   `json:"id"`
	NameZh           string   `json:"name_zh"`
	NameEn           string   `json:"name_en"`
	IsCookingOil     bool     `json:"is_cooking_oil"`
	Grams            float64  `json:"grams"`
	FDCID            int64    `json:"fdc_id"`
	FDCDescription   string   `json:"fdc_description"`
	NutritionVersion string   `json:"-"`
	MatchMethod      string   `json:"match_method"`
	Kcal             float64  `json:"kcal"`
	ProteinG         float64  `json:"protein_g"`
	FatG             float64  `json:"fat_g"`
	CarbG            float64  `json:"carb_g"`
	FiberG           *float64 `json:"fiber_g"`
	SodiumMg         *float64 `json:"sodium_mg"`
	IsProduce        bool     `json:"-"` // not stored; set by the caller for advice facts
}

// FoodItemRow is a stored food item.
type FoodItemRow struct {
	ID              string          `json:"id"`
	NameZh          string          `json:"name_zh"`
	NameEn          string          `json:"name_en"`
	CookingMethod   string          `json:"cooking_method"`
	ModelConfidence *float64        `json:"model_confidence"`
	ModelKcal       *float64        `json:"model_kcal"`
	NeedsGramCheck  bool            `json:"needs_gram_check"`
	Ingredients     []IngredientRow `json:"ingredients"`
}

// MealEntry is a meal entry with its food items.
type MealEntry struct {
	ID                 string
	UserID             string
	Source             string
	RecognitionID      string
	PhotoID            string
	PhotoKey           string
	MealType           string
	EatenAt            time.Time
	UTCOffset          int
	EntryDate          string
	EditedAfterConfirm bool
	FoodItems          []FoodItemRow
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// MealEntryInput is the data of a create or update.
type MealEntryInput struct {
	Source        string
	RecognitionID string
	PhotoID       string
	MealType      string
	EatenAt       time.Time
	UTCOffset     int
	EntryDate     string
	FoodItems     []FoodItemRow
}

func insertFoodItems(ctx context.Context, tx *sql.Tx, entryID string, items []FoodItemRow) error {
	for i, fi := range items {
		fiID := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO food_items (id, meal_entry_id, position, name_zh, name_en, cooking_method, model_confidence, model_kcal, needs_gram_check)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
			fiID, entryID, i, fi.NameZh, fi.NameEn, fi.CookingMethod, fi.ModelConfidence, fi.ModelKcal, fi.NeedsGramCheck); err != nil {
			return err
		}
		for j, in := range fi.Ingredients {
			if _, err := tx.ExecContext(ctx, `INSERT INTO ingredients (id, food_item_id, position, name_zh, name_en, is_cooking_oil, grams, fdc_id, fdc_description,
				nutrition_version, match_method, kcal, protein_g, fat_g, carb_g, fiber_g, sodium_mg)
				VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				newID(), fiID, j, in.NameZh, in.NameEn, in.IsCookingOil, in.Grams, in.FDCID, in.FDCDescription,
				in.NutritionVersion, in.MatchMethod, in.Kcal, in.ProteinG, in.FatG, in.CarbG, in.FiberG, in.SodiumMg); err != nil {
				return err
			}
		}
	}
	return nil
}

func markDayStale(ctx context.Context, tx *sql.Tx, userID string, dates ...string) error {
	for _, d := range dates {
		if _, err := tx.ExecContext(ctx, "UPDATE advice SET stale = 1 WHERE user_id = ? AND kind = 'daily_summary' AND entry_date = ?", userID, d); err != nil {
			return err
		}
	}
	return nil
}

// CreateMealEntry saves a new meal entry. For a recognition it also stores
// the snapshot of the first confirmation (evaluation pair, ADR 0003).
func (s *Store) CreateMealEntry(ctx context.Context, userID string, in MealEntryInput, now time.Time) (string, error) {
	id := newID()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var snapshot any
		if in.RecognitionID != "" {
			var taken int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM meal_entries WHERE recognition_id = ?", in.RecognitionID).Scan(&taken); err != nil {
				return err
			}
			if taken > 0 {
				return ErrAlreadyConfirmed
			}
			b, _ := json.Marshal(in.FoodItems)
			snapshot = string(b)
		}
		nowS := formatTime(now)
		if _, err := tx.ExecContext(ctx, `INSERT INTO meal_entries (id, user_id, source, recognition_id, photo_id, meal_type, eaten_at, utc_offset_minutes, entry_date,
			confirmed_snapshot_json, created_at, updated_at) VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
			id, userID, in.Source, in.RecognitionID, in.PhotoID, in.MealType, formatTime(in.EatenAt), in.UTCOffset, in.EntryDate, snapshot, nowS, nowS); err != nil {
			return err
		}
		if err := insertFoodItems(ctx, tx, id, in.FoodItems); err != nil {
			return err
		}
		return markDayStale(ctx, tx, userID, in.EntryDate)
	})
	return id, err
}

// UpdateMealEntry replaces the food items, meal type, and eaten time.
func (s *Store) UpdateMealEntry(ctx context.Context, userID, id string, in MealEntryInput, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var oldDate string
		err := tx.QueryRowContext(ctx, "SELECT entry_date FROM meal_entries WHERE id = ? AND user_id = ?", id, userID).Scan(&oldDate)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE meal_entries SET meal_type = ?, eaten_at = ?, utc_offset_minutes = ?, entry_date = ?,
			edited_after_confirm = 1, updated_at = ? WHERE id = ?`,
			in.MealType, formatTime(in.EatenAt), in.UTCOffset, in.EntryDate, formatTime(now), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM food_items WHERE meal_entry_id = ?", id); err != nil {
			return err
		}
		if err := insertFoodItems(ctx, tx, id, in.FoodItems); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE advice SET stale = 1 WHERE meal_entry_id = ?", id); err != nil {
			return err
		}
		return markDayStale(ctx, tx, userID, oldDate, in.EntryDate)
	})
}

// DeleteMealEntry deletes the entry, its recognition, and its photo, and
// queues the S3 key (ADR 0007).
func (s *Store) DeleteMealEntry(ctx context.Context, userID, id string, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var date, recID, photoID, key string
		err := tx.QueryRowContext(ctx, `SELECT m.entry_date, COALESCE(m.recognition_id, ''), COALESCE(m.photo_id, ''), COALESCE(p.s3_key, '')
			FROM meal_entries m LEFT JOIN meal_photos p ON p.id = m.photo_id WHERE m.id = ? AND m.user_id = ?`, id, userID).Scan(&date, &recID, &photoID, &key)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM meal_entries WHERE id = ?", id); err != nil {
			return err
		}
		if recID != "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM recognitions WHERE id = ?", recID); err != nil {
				return err
			}
		}
		if photoID != "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM meal_photos WHERE id = ?", photoID); err != nil {
				return err
			}
			if err := queuePhotoDeletion(ctx, tx, now, key); err != nil {
				return err
			}
		}
		return markDayStale(ctx, tx, userID, date)
	})
}

const entryColumns = `m.id, m.user_id, m.source, COALESCE(m.recognition_id, ''), COALESCE(m.photo_id, ''), COALESCE(p.s3_key, ''), m.meal_type,
  m.eaten_at, m.utc_offset_minutes, m.entry_date, m.edited_after_confirm, m.created_at, m.updated_at`

const entryFrom = " FROM meal_entries m LEFT JOIN meal_photos p ON p.id = m.photo_id "

func scanEntry(row interface{ Scan(...any) error }) (MealEntry, error) {
	var e MealEntry
	var eaten, created, updated string
	err := row.Scan(&e.ID, &e.UserID, &e.Source, &e.RecognitionID, &e.PhotoID, &e.PhotoKey, &e.MealType, &eaten, &e.UTCOffset, &e.EntryDate,
		&e.EditedAfterConfirm, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.EatenAt, e.CreatedAt, e.UpdatedAt = parseTime(eaten), parseTime(created), parseTime(updated)
	return e, nil
}

// loadFoodItems fills the food items of the entries.
func (s *Store) loadFoodItems(ctx context.Context, entries []MealEntry) error {
	if len(entries) == 0 {
		return nil
	}
	idx := map[string]int{}
	args := make([]any, len(entries))
	for i, e := range entries {
		idx[e.ID] = i
		args[i] = e.ID
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(entries)), ",")
	rows, err := s.r.QueryContext(ctx, `SELECT f.meal_entry_id, f.id, COALESCE(f.name_zh, ''), COALESCE(f.name_en, ''), f.cooking_method, f.model_confidence, f.model_kcal, f.needs_gram_check,
		COALESCE(i.id, ''), COALESCE(i.name_zh, ''), COALESCE(i.name_en, ''), COALESCE(i.is_cooking_oil, 0), COALESCE(i.grams, 0), COALESCE(i.fdc_id, 0),
		COALESCE(i.fdc_description, ''), COALESCE(i.nutrition_version, ''), COALESCE(i.match_method, ''), COALESCE(i.kcal, 0), COALESCE(i.protein_g, 0),
		COALESCE(i.fat_g, 0), COALESCE(i.carb_g, 0), i.fiber_g, i.sodium_mg
		FROM food_items f LEFT JOIN ingredients i ON i.food_item_id = f.id
		WHERE f.meal_entry_id IN (`+ph+`) ORDER BY f.meal_entry_id, f.position, i.position`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entryID string
		var fi FoodItemRow
		var conf, mk, fiber, sodium sql.NullFloat64
		var in IngredientRow
		if err := rows.Scan(&entryID, &fi.ID, &fi.NameZh, &fi.NameEn, &fi.CookingMethod, &conf, &mk, &fi.NeedsGramCheck,
			&in.ID, &in.NameZh, &in.NameEn, &in.IsCookingOil, &in.Grams, &in.FDCID, &in.FDCDescription, &in.NutritionVersion, &in.MatchMethod,
			&in.Kcal, &in.ProteinG, &in.FatG, &in.CarbG, &fiber, &sodium); err != nil {
			return err
		}
		e := &entries[idx[entryID]]
		if n := len(e.FoodItems); n == 0 || e.FoodItems[n-1].ID != fi.ID {
			if conf.Valid {
				fi.ModelConfidence = &conf.Float64
			}
			if mk.Valid {
				fi.ModelKcal = &mk.Float64
			}
			e.FoodItems = append(e.FoodItems, fi)
		}
		if in.ID != "" {
			if fiber.Valid {
				v := fiber.Float64
				in.FiberG = &v
			}
			if sodium.Valid {
				v := sodium.Float64
				in.SodiumMg = &v
			}
			last := &e.FoodItems[len(e.FoodItems)-1]
			last.Ingredients = append(last.Ingredients, in)
		}
	}
	return rows.Err()
}

func (s *Store) queryEntries(ctx context.Context, query string, args ...any) ([]MealEntry, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+entryColumns+entryFrom+query, args...)
	if err != nil {
		return nil, err
	}
	var out []MealEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.loadFoodItems(ctx, out)
}

// GetMealEntry returns one meal entry of the user.
func (s *Store) GetMealEntry(ctx context.Context, userID, id string) (MealEntry, error) {
	es, err := s.queryEntries(ctx, "WHERE m.id = ? AND m.user_id = ?", id, userID)
	if err != nil {
		return MealEntry{}, err
	}
	if len(es) == 0 {
		return MealEntry{}, ErrNotFound
	}
	return es[0], nil
}

// DayEntries returns the meal entries of a local date, by eaten time.
func (s *Store) DayEntries(ctx context.Context, userID, date string) ([]MealEntry, error) {
	return s.queryEntries(ctx, "WHERE m.user_id = ? AND m.entry_date = ? ORDER BY m.eaten_at, m.id", userID, date)
}

type cursor struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

// ListMealEntries returns entries by eaten_at DESC, id DESC after the cursor,
// and the next cursor ("" at the end).
func (s *Store) ListMealEntries(ctx context.Context, userID, after string, limit int) ([]MealEntry, string, error) {
	q, args := "WHERE m.user_id = ?", []any{userID}
	if after != "" {
		b, err := base64.RawURLEncoding.DecodeString(after)
		var c cursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.T == "" {
			return nil, "", ErrBadCursor
		}
		q += " AND (m.eaten_at < ? OR (m.eaten_at = ? AND m.id < ?))"
		args = append(args, c.T, c.T, c.ID)
	}
	q += " ORDER BY m.eaten_at DESC, m.id DESC LIMIT ?"
	args = append(args, limit+1)
	es, err := s.queryEntries(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(es) > limit {
		es = es[:limit]
		last := es[len(es)-1]
		b, _ := json.Marshal(cursor{T: formatTime(last.EatenAt), ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return es, next, nil
}

// DayTotal is the energy of one date.
type DayTotal struct {
	Date       string
	Kcal       float64
	EntryCount int
}

// DayTotals returns per-day energy for dates in [from, to] with entries.
func (s *Store) DayTotals(ctx context.Context, userID, from, to string) ([]DayTotal, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT m.entry_date, COALESCE(SUM(i.kcal), 0), COUNT(DISTINCT m.id)
		FROM meal_entries m LEFT JOIN food_items f ON f.meal_entry_id = m.id LEFT JOIN ingredients i ON i.food_item_id = f.id
		WHERE m.user_id = ? AND m.entry_date BETWEEN ? AND ? GROUP BY m.entry_date ORDER BY m.entry_date`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayTotal
	for rows.Next() {
		var d DayTotal
		if err := rows.Scan(&d.Date, &d.Kcal, &d.EntryCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// IdempotentResponse is a stored response for POST /v1/meal-entries.
type IdempotentResponse struct {
	RequestHash string
	Status      int
	Body        string
}

// GetIdempotent returns a stored response, or ErrNotFound.
func (s *Store) GetIdempotent(ctx context.Context, userID, key string) (IdempotentResponse, error) {
	var r IdempotentResponse
	err := s.r.QueryRowContext(ctx, "SELECT request_hash, status_code, response_json FROM idempotency_keys WHERE user_id = ? AND key = ?", userID, key).
		Scan(&r.RequestHash, &r.Status, &r.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// SaveIdempotent stores a response. An existing key is kept.
func (s *Store) SaveIdempotent(ctx context.Context, userID, key string, r IdempotentResponse, now time.Time) error {
	_, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency_keys (user_id, key, request_hash, status_code, response_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, key, r.RequestHash, r.Status, r.Body, formatTime(now))
	return err
}

// DeleteOldIdempotencyKeys removes stored responses older than cutoff.
func (s *Store) DeleteOldIdempotencyKeys(ctx context.Context, cutoff time.Time) error {
	_, err := s.w.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE created_at < ?", formatTime(cutoff))
	return err
}
