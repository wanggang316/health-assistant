package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/wanggang316/health-assistant/server/internal/localtime"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/photo"
	"github.com/wanggang316/health-assistant/server/internal/quota"
	"github.com/wanggang316/health-assistant/server/internal/recognition"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

type recognitionResponse struct {
	ID                string                       `json:"id"`
	Status            string                       `json:"status"`
	IsFood            bool                         `json:"is_food"`
	PhotoURL          *string                      `json:"photo_url"`
	SuggestedMealType string                       `json:"suggested_meal_type"`
	FoodItems         []recognition.FoodItemResult `json:"food_items"`
	Nutrients         nutrition.Nutrients          `json:"nutrients"`
	MealEntryID       *string                      `json:"meal_entry_id"`
	RecognitionQuota  *quotaJSON                   `json:"recognition_quota,omitempty"`
}

func (s *Server) photoURL(r *http.Request, key string) *string {
	if key == "" {
		return nil
	}
	u, err := s.photos.PresignGet(r.Context(), key)
	if err != nil {
		s.log.ErrorContext(r.Context(), "presign", "err", err)
		return nil
	}
	return &u
}

func (s *Server) toRecognitionResponse(r *http.Request, rec store.Recognition, res recognition.Result) recognitionResponse {
	if res.FoodItems == nil {
		res.FoodItems = []recognition.FoodItemResult{}
	}
	return recognitionResponse{
		ID: rec.ID, Status: rec.Status, IsFood: res.IsFood, PhotoURL: s.photoURL(r, rec.PhotoKey),
		SuggestedMealType: res.SuggestedMealType, FoodItems: res.FoodItems, Nutrients: res.Nutrients,
		MealEntryID: optional(rec.MealEntryID),
	}
}

var (
	errQuotaExceeded      = &apiError{Status: http.StatusTooManyRequests, Code: "quota_exceeded", Message: "Daily recognition quota is used up."}
	errModelUnavailable   = &apiError{Status: http.StatusServiceUnavailable, Code: "model_unavailable", Message: "The recognition model is not available. Try again later."}
	errModelOutputInvalid = &apiError{Status: http.StatusBadGateway, Code: "model_output_invalid", Message: "The recognition result was not usable. Try again."}
	errKeyReused          = &apiError{Status: http.StatusConflict, Code: "idempotency_key_reused", Message: "Idempotency-Key was used for a different request."}
	errInProgress         = &apiError{Status: http.StatusConflict, Code: "recognition_in_progress", Message: "The first request with this Idempotency-Key is still running."}
)

func idempotencyKey(r *http.Request) (string, error) {
	k := r.Header.Get("Idempotency-Key")
	if _, err := uuid.Parse(k); err != nil {
		return "", errInvalid("Idempotency-Key", "Idempotency-Key must be a UUID.")
	}
	return k, nil
}

func (s *Server) createRecognition(w http.ResponseWriter, r *http.Request) error {
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, photo.MaxBytes+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		return errInvalid("body", "Body must be multipart/form-data.")
	}
	var data []byte
	var meta recognition.Meta
	hasMeta := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge
		}
		if err != nil {
			return errInvalid("body", "Bad multipart body.")
		}
		switch part.FormName() {
		case "photo":
			data, err = io.ReadAll(io.LimitReader(part, photo.MaxBytes+1))
			if errors.As(err, &mbe) {
				return errTooLarge
			}
			if err != nil {
				return errInvalid("photo", "Cannot read photo.")
			}
			if len(data) > photo.MaxBytes {
				return errTooLarge
			}
		case "meta":
			if err := json.NewDecoder(io.LimitReader(part, 4<<10)).Decode(&meta); err != nil {
				return errInvalid("meta", "meta must be JSON with taken_at and utc_offset_minutes.")
			}
			hasMeta = true
		}
	}
	if data == nil {
		return errInvalid("photo", "photo is required.")
	}
	if !hasMeta || meta.TakenAt.IsZero() {
		return errInvalid("meta", "meta.taken_at is required.")
	}
	if !localtime.ValidOffset(meta.UTCOffsetMinutes) {
		return errInvalid("meta", "meta.utc_offset_minutes must be from -720 to 840.")
	}
	now := s.now()
	if meta.TakenAt.After(now.Add(10*time.Minute)) || meta.TakenAt.Before(now.Add(-30*24*time.Hour)) {
		return errInvalid("meta", "meta.taken_at must be within the last 30 days.")
	}
	p, err := photo.Process(data)
	if err != nil {
		return &apiError{Status: http.StatusBadRequest, Code: "invalid_photo", Message: err.Error()}
	}
	userID := principal(r.Context()).UserID
	out, err := s.recognition.Recognize(r.Context(), userID, key, p, recognition.RequestHash(data, meta), meta)
	switch {
	case errors.Is(err, store.ErrQuotaExceeded):
		_, resets := quota.Day(now, meta.UTCOffsetMinutes)
		e := *errQuotaExceeded
		e.Details = map[string]any{"limit": out.QuotaLimit, "resets_at": resets}
		return &e
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		return errKeyReused
	case errors.Is(err, store.ErrRecognitionInProgress):
		return errInProgress
	case errors.Is(err, recognition.ErrModelUnavailable):
		return errModelUnavailable
	case errors.Is(err, recognition.ErrModelOutputInvalid):
		return errModelOutputInvalid
	case err != nil:
		return err
	}
	resp := s.toRecognitionResponse(r, out.Recognition, out.Result)
	resp.RecognitionQuota = &quotaJSON{Limit: out.QuotaLimit, Used: out.QuotaUsed, ResetsAt: out.ResetsAt}
	status := http.StatusCreated
	if out.Replay {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
	return nil
}

func (s *Server) getRecognition(w http.ResponseWriter, r *http.Request) error {
	rec, err := s.store.GetRecognition(r.Context(), principal(r.Context()).UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	var res recognition.Result
	if rec.ResultJSON != "" {
		if err := json.Unmarshal([]byte(rec.ResultJSON), &res); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, s.toRecognitionResponse(r, rec, res))
	return nil
}
