package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/advice"
	"github.com/wanggang316/health-assistant/server/internal/meals"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/store"
	"github.com/wanggang316/health-assistant/server/internal/target"
)

type ingredientJSON struct {
	ID             string              `json:"id"`
	NameZh         string              `json:"name_zh"`
	NameEn         string              `json:"name_en"`
	Grams          float64             `json:"grams"`
	FDCID          int64               `json:"fdc_id"`
	FDCDescription string              `json:"fdc_description"`
	IsCookingOil   bool                `json:"is_cooking_oil"`
	MatchMethod    string              `json:"match_method"`
	Nutrients      nutrition.Nutrients `json:"nutrients"`
}

type foodItemJSON struct {
	ID             string              `json:"id"`
	NameZh         string              `json:"name_zh"`
	NameEn         string              `json:"name_en"`
	CookingMethod  string              `json:"cooking_method"`
	NeedsGramCheck bool                `json:"needs_gram_check"`
	Ingredients    []ingredientJSON    `json:"ingredients,omitempty"`
	Nutrients      nutrition.Nutrients `json:"nutrients"`
}

type mealEntryJSON struct {
	ID               string              `json:"id"`
	Source           string              `json:"source"`
	RecognitionID    *string             `json:"recognition_id"`
	MealType         string              `json:"meal_type"`
	EatenAt          time.Time           `json:"eaten_at"`
	UTCOffsetMinutes int                 `json:"utc_offset_minutes"`
	EntryDate        string              `json:"entry_date"`
	PhotoURL         *string             `json:"photo_url"`
	FoodItems        []foodItemJSON      `json:"food_items"`
	Nutrients        nutrition.Nutrients `json:"nutrients"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

func (s *Server) toMealEntryJSON(r *http.Request, e store.MealEntry, withIngredients bool) mealEntryJSON {
	out := mealEntryJSON{
		ID: e.ID, Source: e.Source, RecognitionID: optional(e.RecognitionID), MealType: e.MealType, EatenAt: e.EatenAt.UTC(),
		UTCOffsetMinutes: e.UTCOffset, EntryDate: e.EntryDate, PhotoURL: s.photoURL(r, e.PhotoKey), FoodItems: []foodItemJSON{},
		Nutrients: meals.Totals(e).Rounded(), CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	}
	for _, fi := range e.FoodItems {
		item := foodItemJSON{ID: fi.ID, NameZh: fi.NameZh, NameEn: fi.NameEn, CookingMethod: fi.CookingMethod,
			NeedsGramCheck: fi.NeedsGramCheck, Nutrients: meals.ItemTotals(fi).Rounded()}
		if withIngredients {
			item.Ingredients = []ingredientJSON{}
			for _, in := range fi.Ingredients {
				item.Ingredients = append(item.Ingredients, ingredientJSON{
					ID: in.ID, NameZh: in.NameZh, NameEn: in.NameEn, Grams: in.Grams, FDCID: in.FDCID, FDCDescription: in.FDCDescription,
					IsCookingOil: in.IsCookingOil, MatchMethod: in.MatchMethod, Nutrients: meals.IngredientNutrients(in).Rounded(),
				})
			}
		}
		out.FoodItems = append(out.FoodItems, item)
	}
	return out
}

func mapMealError(err error) error {
	var inv *meals.InvalidError
	var unres *meals.UnresolvedError
	switch {
	case errors.As(err, &inv):
		return errInvalid(inv.Field, inv.Message)
	case errors.As(err, &unres):
		return &apiError{Status: http.StatusUnprocessableEntity, Code: "unresolved_ingredient", Message: unres.Error(),
			Details: map[string]any{"food_item": unres.FoodItem, "ingredient": unres.Ingredient}}
	case errors.Is(err, meals.ErrRecognitionNotFound), errors.Is(err, store.ErrNotFound):
		return errNotFound
	}
	return err
}

// readMealInput reads the body and returns it with its hash.
func readMealInput(w http.ResponseWriter, r *http.Request) (meals.Input, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return meals.Input{}, "", errTooLarge
	}
	var in meals.Input
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, "", &apiError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Body is not valid JSON: " + err.Error()}
	}
	sum := sha256.Sum256(raw)
	return in, hex.EncodeToString(sum[:]), nil
}

func (s *Server) createMealEntry(w http.ResponseWriter, r *http.Request) error {
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	in, hash, err := readMealInput(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	userID := principal(ctx).UserID
	if prev, err := s.store.GetIdempotent(ctx, userID, key); err == nil {
		if prev.RequestHash != hash {
			return errKeyReused
		}
		// Replay: fresh photo URL, so build the response again if the entry still exists.
		var stored mealEntryJSON
		if json.Unmarshal([]byte(prev.Body), &stored) == nil {
			if e, err := s.store.GetMealEntry(ctx, userID, stored.ID); err == nil {
				writeJSON(w, prev.Status, s.toMealEntryJSON(r, e, true))
				return nil
			}
		}
		return errNotFound
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	e, err := s.meals.Create(ctx, userID, in)
	if errors.Is(err, store.ErrAlreadyConfirmed) {
		return &apiError{Status: http.StatusConflict, Code: "recognition_already_confirmed", Message: "The recognition already has a meal entry.",
			Details: map[string]any{"meal_entry_id": e.ID}}
	}
	if err != nil {
		return mapMealError(err)
	}
	resp := s.toMealEntryJSON(r, e, true)
	body, _ := json.Marshal(resp)
	if err := s.store.SaveIdempotent(ctx, userID, key, store.IdempotentResponse{RequestHash: hash, Status: http.StatusCreated, Body: string(body)}, s.now()); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (s *Server) updateMealEntry(w http.ResponseWriter, r *http.Request) error {
	in, _, err := readMealInput(w, r)
	if err != nil {
		return err
	}
	if in.RecognitionID != "" {
		return errInvalid("recognition_id", "recognition_id cannot change.")
	}
	e, err := s.meals.Update(r.Context(), principal(r.Context()).UserID, r.PathValue("id"), in)
	if err != nil {
		return mapMealError(err)
	}
	writeJSON(w, http.StatusOK, s.toMealEntryJSON(r, e, true))
	return nil
}

func (s *Server) getMealEntry(w http.ResponseWriter, r *http.Request) error {
	e, err := s.store.GetMealEntry(r.Context(), principal(r.Context()).UserID, r.PathValue("id"))
	if err != nil {
		return mapMealError(err)
	}
	writeJSON(w, http.StatusOK, s.toMealEntryJSON(r, e, true))
	return nil
}

func (s *Server) deleteMealEntry(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	userID := principal(ctx).UserID
	e, err := s.store.GetMealEntry(ctx, userID, r.PathValue("id"))
	if err != nil {
		return mapMealError(err)
	}
	if err := s.store.DeleteMealEntry(ctx, userID, e.ID, s.now()); err != nil {
		return mapMealError(err)
	}
	if e.PhotoKey != "" {
		derr := s.photos.Delete(ctx, e.PhotoKey)
		if ferr := s.store.FinishPhotoDeletion(ctx, e.PhotoKey, derr); ferr != nil {
			s.log.ErrorContext(ctx, "photo deletion queue", "err", ferr)
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listMealEntries(w http.ResponseWriter, r *http.Request) error {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			return errInvalid("limit", "limit must be from 1 to 50.")
		}
		limit = n
	}
	es, next, err := s.store.ListMealEntries(r.Context(), principal(r.Context()).UserID, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, store.ErrBadCursor) {
		return errInvalid("cursor", "Bad cursor.")
	}
	if err != nil {
		return err
	}
	out := make([]mealEntryJSON, 0, len(es))
	for _, e := range es {
		out = append(out, s.toMealEntryJSON(r, e, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"meal_entries": out, "next_cursor": optional(next)})
	return nil
}

func (s *Server) mealFeedback(w http.ResponseWriter, r *http.Request) error {
	t, err := s.advice.MealFeedback(r.Context(), principal(r.Context()).UserID, r.PathValue("id"), locale(r))
	if err != nil {
		return mapMealError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": advice.KindMealFeedback, "text": t.Text, "generated_at": t.GeneratedAt.UTC(), "disclaimer_required": true})
	return nil
}

func parseDate(field, v string) (string, error) {
	if _, err := time.Parse(time.DateOnly, v); err != nil {
		return "", errInvalid(field, field+" must be YYYY-MM-DD.")
	}
	return v, nil
}

type summaryJSON struct {
	State       string     `json:"state"`
	Text        *string    `json:"text"`
	GeneratedAt *time.Time `json:"generated_at"`
}

func (s *Server) getDay(w http.ResponseWriter, r *http.Request) error {
	date, err := parseDate("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	userID := principal(ctx).UserID
	entries, err := s.store.DayEntries(ctx, userID, date)
	if err != nil {
		return err
	}
	tgt, err := s.advice.DailyTarget(ctx, userID)
	if err != nil {
		return err
	}
	state, text, err := s.advice.SummaryState(ctx, userID, date, locale(r), entries, tgt)
	if err != nil {
		return err
	}
	sum := summaryJSON{State: state}
	if text != nil {
		g := text.GeneratedAt.UTC()
		sum.Text, sum.GeneratedAt = &text.Text, &g
	}
	out := make([]mealEntryJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, s.toMealEntryJSON(r, e, false))
	}
	writeJSON(w, http.StatusOK, struct {
		Date         string              `json:"date"`
		DailyTarget  *target.Daily       `json:"daily_target"`
		Totals       nutrition.Nutrients `json:"totals"`
		MealEntries  []mealEntryJSON     `json:"meal_entries"`
		DailySummary summaryJSON         `json:"daily_summary"`
	}{date, tgt, meals.Totals(entries...).Rounded(), out, sum})
	return nil
}

func (s *Server) getDaySummary(w http.ResponseWriter, r *http.Request) error {
	date, err := parseDate("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	t, err := s.advice.DailySummary(r.Context(), principal(r.Context()).UserID, date, locale(r))
	if errors.Is(err, advice.ErrNoEntries) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": advice.KindDailySummary, "date": date, "text": t.Text, "generated_at": t.GeneratedAt.UTC(), "disclaimer_required": true})
	return nil
}

func (s *Server) listDays(w http.ResponseWriter, r *http.Request) error {
	from, err := parseDate("from", r.URL.Query().Get("from"))
	if err != nil {
		return err
	}
	to, err := parseDate("to", r.URL.Query().Get("to"))
	if err != nil {
		return err
	}
	f, _ := time.Parse(time.DateOnly, from)
	t, _ := time.Parse(time.DateOnly, to)
	if t.Before(f) || t.Sub(f) > 61*24*time.Hour {
		return errInvalid("to", "The range must be 1 to 62 days.")
	}
	totals, err := s.store.DayTotals(r.Context(), principal(r.Context()).UserID, from, to)
	if err != nil {
		return err
	}
	type day struct {
		Date       string  `json:"date"`
		Kcal       float64 `json:"kcal"`
		EntryCount int     `json:"entry_count"`
	}
	out := make([]day, 0, len(totals))
	for _, d := range totals {
		out = append(out, day{d.Date, float64(int(d.Kcal*10+0.5)) / 10, d.EntryCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
	return nil
}
