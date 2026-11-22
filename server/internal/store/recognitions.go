package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Errors of ReserveRecognition.
var (
	ErrQuotaExceeded         = errors.New("recognition quota exceeded")
	ErrGlobalCap             = errors.New("global recognition cap reached")
	ErrIdempotencyKeyReused  = errors.New("idempotency key reused with a different request")
	ErrRecognitionInProgress = errors.New("recognition in progress")
)

// Recognition is a row of recognitions.
type Recognition struct {
	ID               string
	UserID           string
	IdempotencyKey   string
	RequestHash      string
	PhotoID          string
	PhotoKey         string
	Status           string
	IsFood           bool
	Model            string
	NutritionVersion string
	ModelOutputJSON  string
	ResultJSON       string
	ErrorCode        string
	TakenAt          time.Time
	UTCOffset        int
	QuotaDate        string
	CreatedAt        time.Time
	MealEntryID      string // set when a meal entry confirmed it
}

// NewRecognition is the input of ReserveRecognition.
type NewRecognition struct {
	UserID         string
	IdempotencyKey string
	RequestHash    string
	Model          string
	TakenAt        time.Time
	UTCOffset      int
	QuotaDate      string
	UTCDate        string // for the global cap
	QuotaLimit     int
	GlobalLimit    int
}

// StalePending is the age after which a pending recognition is treated as
// stopped (the pipeline has a 60 s limit).
const StalePending = 2 * time.Minute

// ReserveResult says what ReserveRecognition did.
type ReserveResult struct {
	Recognition Recognition
	Replay      bool // a completed recognition with the same key and body
	QuotaUsed   int
}

const recognitionColumns = `r.id, r.user_id, r.idempotency_key, r.request_hash, COALESCE(r.photo_id, ''), COALESCE(p.s3_key, ''), r.status,
  COALESCE(r.is_food, 0), r.model, COALESCE(r.nutrition_version, ''), COALESCE(r.model_output_json, ''), COALESCE(r.result_json, ''),
  COALESCE(r.error_code, ''), r.taken_at, r.utc_offset_minutes, r.quota_date, r.created_at, COALESCE(m.id, '')`

const recognitionFrom = ` FROM recognitions r LEFT JOIN meal_photos p ON p.id = r.photo_id LEFT JOIN meal_entries m ON m.recognition_id = r.id `

func scanRecognition(row interface{ Scan(...any) error }) (Recognition, error) {
	var r Recognition
	var taken, created string
	err := row.Scan(&r.ID, &r.UserID, &r.IdempotencyKey, &r.RequestHash, &r.PhotoID, &r.PhotoKey, &r.Status, &r.IsFood, &r.Model,
		&r.NutritionVersion, &r.ModelOutputJSON, &r.ResultJSON, &r.ErrorCode, &taken, &r.UTCOffset, &r.QuotaDate, &created, &r.MealEntryID)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.TakenAt, r.CreatedAt = parseTime(taken), parseTime(created)
	return r, nil
}

// GetRecognition returns a recognition of the user.
func (s *Store) GetRecognition(ctx context.Context, userID, id string) (Recognition, error) {
	return scanRecognition(s.r.QueryRowContext(ctx, "SELECT "+recognitionColumns+recognitionFrom+"WHERE r.id = ? AND r.user_id = ?", id, userID))
}

func quotaUsed(ctx context.Context, tx *sql.Tx, userID, date string) (int, error) {
	var used int
	err := tx.QueryRowContext(ctx, "SELECT used FROM recognition_quota WHERE user_id = ? AND quota_date = ?", userID, date).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return used, err
}

// ReserveRecognition checks the idempotency key, the user quota, and the
// global cap, then inserts a pending recognition and counts it, all in one
// transaction (§6.1 step 2). A failed recognition with the same key and body
// runs again.
func (s *Store) ReserveRecognition(ctx context.Context, n NewRecognition, now time.Time) (ReserveResult, error) {
	var res ReserveResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		existing, err := scanRecognition(tx.QueryRowContext(ctx, "SELECT "+recognitionColumns+recognitionFrom+"WHERE r.user_id = ? AND r.idempotency_key = ?", n.UserID, n.IdempotencyKey))
		retry, stale := false, false
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return err
		case existing.RequestHash != n.RequestHash:
			return ErrIdempotencyKeyReused
		case existing.Status == "pending" && now.Sub(existing.CreatedAt) < StalePending:
			return ErrRecognitionInProgress
		case existing.Status == "pending":
			// The process stopped during the pipeline. The quota is
			// still counted, so run again without counting it twice.
			retry, stale = true, true
		case existing.Status == "succeeded":
			res.Recognition, res.Replay = existing, true
			res.QuotaUsed, err = quotaUsed(ctx, tx, n.UserID, existing.QuotaDate)
			return err
		default: // failed: run again
			retry = true
		}
		used, err := quotaUsed(ctx, tx, n.UserID, n.QuotaDate)
		if err != nil {
			return err
		}
		if !stale && used >= n.QuotaLimit {
			return ErrQuotaExceeded
		}
		var global int
		err = tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(requests), 0) FROM model_usage_daily WHERE usage_date = ? AND purpose = 'recognition'", n.UTCDate).Scan(&global)
		if err != nil {
			return err
		}
		if global >= n.GlobalLimit {
			return ErrGlobalCap
		}
		nowS := formatTime(now)
		if retry {
			_, err = tx.ExecContext(ctx, `UPDATE recognitions SET status = 'pending', error_code = NULL, attempts = 0, model = ?,
				taken_at = ?, utc_offset_minutes = ?, quota_date = ?, created_at = ?, completed_at = NULL WHERE id = ?`,
				n.Model, formatTime(n.TakenAt), n.UTCOffset, n.QuotaDate, nowS, existing.ID)
			res.Recognition = existing
		} else {
			res.Recognition = Recognition{ID: newID()}
			_, err = tx.ExecContext(ctx, `INSERT INTO recognitions (id, user_id, idempotency_key, request_hash, status, model, prompt_version,
				taken_at, utc_offset_minutes, quota_date, created_at) VALUES (?, ?, ?, ?, 'pending', ?, '', ?, ?, ?, ?)`,
				res.Recognition.ID, n.UserID, n.IdempotencyKey, n.RequestHash, n.Model, formatTime(n.TakenAt), n.UTCOffset, n.QuotaDate, nowS)
		}
		if err != nil {
			return err
		}
		res.Recognition.UserID, res.Recognition.Status, res.Recognition.QuotaDate = n.UserID, "pending", n.QuotaDate
		res.Recognition.TakenAt, res.Recognition.UTCOffset = n.TakenAt, n.UTCOffset
		if stale {
			res.QuotaUsed = used
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO recognition_quota (user_id, quota_date, used) VALUES (?, ?, 1)
			ON CONFLICT (user_id, quota_date) DO UPDATE SET used = used + 1`, n.UserID, n.QuotaDate); err != nil {
			return err
		}
		res.QuotaUsed = used + 1
		return addUsage(ctx, tx, n.UTCDate, n.Model, "recognition", 1, 0, 0)
	})
	return res, err
}

func addUsage(ctx context.Context, tx *sql.Tx, date, model, purpose string, requests, in, out int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO model_usage_daily (usage_date, model, purpose, requests, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (usage_date, model, purpose) DO UPDATE SET requests = requests + excluded.requests,
		input_tokens = input_tokens + excluded.input_tokens, output_tokens = output_tokens + excluded.output_tokens`,
		date, model, purpose, requests, in, out)
	return err
}

// AddModelUsage records the tokens of a model call. requests is 0 for a
// recognition, which ReserveRecognition already counted.
func (s *Store) AddModelUsage(ctx context.Context, date, model, purpose string, requests, in, out int) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return addUsage(ctx, tx, date, model, purpose, requests, in, out) })
}

// CompletedRecognition is the data of a succeeded recognition.
type CompletedRecognition struct {
	PhotoKey         string
	PhotoWidth       int
	PhotoHeight      int
	PhotoBytes       int
	IsFood           bool
	PromptVersion    string
	NutritionVersion string
	ModelOutputJSON  string
	ResultJSON       string
	Attempts         int
	InputTokens      int
	OutputTokens     int
	ThoughtTokens    int
	LatencyMs        int64
}

// CompleteRecognition stores the photo row and the result.
func (s *Store) CompleteRecognition(ctx context.Context, userID, id, photoID string, c CompletedRecognition, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		nowS := formatTime(now)
		if _, err := tx.ExecContext(ctx, "INSERT INTO meal_photos (id, user_id, s3_key, width, height, bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			photoID, userID, c.PhotoKey, c.PhotoWidth, c.PhotoHeight, c.PhotoBytes, nowS); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE recognitions SET status = 'succeeded', photo_id = ?, is_food = ?, prompt_version = ?, nutrition_version = ?,
			model_output_json = ?, result_json = ?, attempts = ?, input_tokens = ?, output_tokens = ?, thought_tokens = ?, latency_ms = ?, completed_at = ?
			WHERE id = ? AND user_id = ?`,
			photoID, c.IsFood, c.PromptVersion, c.NutritionVersion, c.ModelOutputJSON, c.ResultJSON, c.Attempts,
			c.InputTokens, c.OutputTokens, c.ThoughtTokens, c.LatencyMs, nowS, id, userID)
		return err
	})
}

// FailRecognition marks a recognition failed and refunds the quota.
func (s *Store) FailRecognition(ctx context.Context, userID, id, code string, attempts int, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var date string
		if err := tx.QueryRowContext(ctx, "SELECT quota_date FROM recognitions WHERE id = ? AND user_id = ? AND status = 'pending'", id, userID).Scan(&date); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE recognitions SET status = 'failed', error_code = ?, attempts = ?, completed_at = ? WHERE id = ?",
			code, attempts, formatTime(now), id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE recognition_quota SET used = MAX(used - 1, 0) WHERE user_id = ? AND quota_date = ?", userID, date)
		return err
	})
}

// QueuePhotoDeletion adds S3 keys to the retry queue.
func (s *Store) QueuePhotoDeletion(ctx context.Context, keys ...string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return queuePhotoDeletion(ctx, tx, time.Now(), keys...) })
}

func queuePhotoDeletion(ctx context.Context, tx *sql.Tx, now time.Time, keys ...string) error {
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO photo_deletions (s3_key, created_at) VALUES (?, ?)", k, formatTime(now)); err != nil {
			return err
		}
	}
	return nil
}

// PendingPhotoDeletions returns up to limit queued keys.
func (s *Store) PendingPhotoDeletions(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT s3_key FROM photo_deletions ORDER BY created_at LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// FinishPhotoDeletion removes a key from the queue, or records the error.
func (s *Store) FinishPhotoDeletion(ctx context.Context, key string, deleteErr error) error {
	if deleteErr == nil {
		_, err := s.w.ExecContext(ctx, "DELETE FROM photo_deletions WHERE s3_key = ?", key)
		return err
	}
	_, err := s.w.ExecContext(ctx, "UPDATE photo_deletions SET attempts = attempts + 1, last_error = ? WHERE s3_key = ?", deleteErr.Error(), key)
	return err
}

// DeleteUnconfirmedRecognitions deletes recognitions without a meal entry
// that were created before cutoff, with their photos, and queues the S3 keys
// (7 days, §4 notes).
func (s *Store) DeleteUnconfirmedRecognitions(ctx context.Context, cutoff time.Time) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT r.id, COALESCE(r.photo_id, ''), COALESCE(p.s3_key, '') FROM recognitions r
			LEFT JOIN meal_photos p ON p.id = r.photo_id
			WHERE r.created_at < ? AND NOT EXISTS (SELECT 1 FROM meal_entries m WHERE m.recognition_id = r.id)`, formatTime(cutoff))
		if err != nil {
			return err
		}
		type item struct{ id, photoID, key string }
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.photoID, &it.key); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := time.Now()
		for _, it := range items {
			if _, err := tx.ExecContext(ctx, "DELETE FROM recognitions WHERE id = ?", it.id); err != nil {
				return err
			}
			if it.photoID != "" {
				if _, err := tx.ExecContext(ctx, "DELETE FROM meal_photos WHERE id = ?", it.photoID); err != nil {
					return err
				}
				if err := queuePhotoDeletion(ctx, tx, now, it.key); err != nil {
					return err
				}
			}
		}
		n = len(items)
		return nil
	})
	return n, err
}
