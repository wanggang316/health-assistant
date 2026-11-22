// Package meals creates and edits meal entries. The server recalculates all
// nutrients from nutrition.db; client values are never stored (ADR 0002).
package meals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/localtime"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/recognition"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

// Limits of a meal entry.
const (
	MaxFoodItems   = 20
	MaxIngredients = 30
	MaxGrams       = 5000
	MaxNameRunes   = 100
)

// Errors returned by Service.
var (
	ErrRecognitionNotFound = errors.New("recognition not found")
)

// InvalidError is a bad field.
type InvalidError struct{ Field, Message string }

func (e *InvalidError) Error() string { return e.Field + ": " + e.Message }

// UnresolvedError means an ingredient has no known food (422).
type UnresolvedError struct{ FoodItem, Ingredient int }

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("food_items[%d].ingredients[%d] has no fdc_id", e.FoodItem, e.Ingredient)
}

// IngredientInput is one ingredient from the client.
type IngredientInput struct {
	NameZh       string  `json:"name_zh"`
	NameEn       string  `json:"name_en"`
	Grams        float64 `json:"grams"`
	FDCID        int64   `json:"fdc_id"`
	IsCookingOil bool    `json:"is_cooking_oil"`
}

// FoodItemInput is one food item from the client.
type FoodItemInput struct {
	NameZh        string            `json:"name_zh"`
	NameEn        string            `json:"name_en"`
	CookingMethod string            `json:"cooking_method"`
	Ingredients   []IngredientInput `json:"ingredients"`
}

// Input is the body of POST and PUT /v1/meal-entries.
type Input struct {
	RecognitionID    string          `json:"recognition_id"`
	MealType         string          `json:"meal_type"`
	EatenAt          time.Time       `json:"eaten_at"`
	UTCOffsetMinutes int             `json:"utc_offset_minutes"`
	FoodItems        []FoodItemInput `json:"food_items"`
}

// Service creates and edits meal entries.
type Service struct {
	Store     *store.Store
	Nutrition *nutrition.DB
	Now       func() time.Time
}

func validName(field, s string) error {
	if len([]rune(s)) > MaxNameRunes {
		return &InvalidError{field, "name is too long"}
	}
	return nil
}

func (s *Service) validate(in Input) error {
	if !localtime.ValidMealType(in.MealType) {
		return &InvalidError{"meal_type", "meal_type must be breakfast, lunch, dinner, or snack"}
	}
	if in.EatenAt.IsZero() {
		return &InvalidError{"eaten_at", "eaten_at is required"}
	}
	if in.EatenAt.After(s.Now().Add(24 * time.Hour)) {
		return &InvalidError{"eaten_at", "eaten_at is in the future"}
	}
	if !localtime.ValidOffset(in.UTCOffsetMinutes) {
		return &InvalidError{"utc_offset_minutes", "utc_offset_minutes must be from -720 to 840"}
	}
	if len(in.FoodItems) == 0 || len(in.FoodItems) > MaxFoodItems {
		return &InvalidError{"food_items", fmt.Sprintf("food_items must have 1 to %d items", MaxFoodItems)}
	}
	methods := map[string]bool{}
	for _, m := range recognition.CookingMethods {
		methods[m] = true
	}
	for i, fi := range in.FoodItems {
		f := fmt.Sprintf("food_items[%d]", i)
		if strings.TrimSpace(fi.NameZh) == "" && strings.TrimSpace(fi.NameEn) == "" {
			return &InvalidError{f, "a food item needs a name"}
		}
		if err := validName(f, fi.NameZh+fi.NameEn); err != nil {
			return err
		}
		if !methods[fi.CookingMethod] {
			return &InvalidError{f + ".cooking_method", "unknown cooking_method"}
		}
		if len(fi.Ingredients) == 0 || len(fi.Ingredients) > MaxIngredients {
			return &InvalidError{f + ".ingredients", fmt.Sprintf("needs 1 to %d ingredients", MaxIngredients)}
		}
		for j, in := range fi.Ingredients {
			g := fmt.Sprintf("%s.ingredients[%d]", f, j)
			if in.Grams <= 0 || in.Grams > MaxGrams {
				return &InvalidError{g + ".grams", "grams must be more than 0 and at most 5000"}
			}
			if err := validName(g, in.NameZh+in.NameEn); err != nil {
				return err
			}
		}
	}
	return nil
}

// build validates the input and recalculates every ingredient.
func (s *Service) build(ctx context.Context, in Input, rec *recognition.Result) ([]store.FoodItemRow, error) {
	if err := s.validate(in); err != nil {
		return nil, err
	}
	var items []store.FoodItemRow
	for i, fi := range in.FoodItems {
		row := store.FoodItemRow{NameZh: strings.TrimSpace(fi.NameZh), NameEn: strings.TrimSpace(fi.NameEn), CookingMethod: fi.CookingMethod}
		var recItem *recognition.FoodItemResult
		if rec != nil && i < len(rec.FoodItems) {
			recItem = &rec.FoodItems[i]
			conf, mk := recItem.Confidence, recItem.ModelKcal
			row.ModelConfidence, row.ModelKcal = &conf, &mk
		}
		unchanged := recItem != nil && len(recItem.Ingredients) == len(fi.Ingredients)
		for j, ing := range fi.Ingredients {
			if ing.FDCID <= 0 {
				return nil, &UnresolvedError{i, j}
			}
			food, err := s.Nutrition.Food(ctx, ing.FDCID)
			if errors.Is(err, nutrition.ErrNotFound) {
				return nil, &UnresolvedError{i, j}
			}
			if err != nil {
				return nil, err
			}
			n := food.Per100g.ForGrams(ing.Grams)
			method := nutrition.MethodUser
			if recItem != nil && j < len(recItem.Ingredients) {
				ri := recItem.Ingredients[j]
				if ri.Food != nil && ri.Food.FDCID == ing.FDCID && ri.MatchMethod != nil {
					method = *ri.MatchMethod
				}
				if ri.Food == nil || ri.Food.FDCID != ing.FDCID || ri.Grams != ing.Grams {
					unchanged = false
				}
			}
			row.Ingredients = append(row.Ingredients, store.IngredientRow{
				NameZh: strings.TrimSpace(ing.NameZh), NameEn: strings.TrimSpace(ing.NameEn), IsCookingOil: ing.IsCookingOil,
				Grams: ing.Grams, FDCID: food.FDCID, FDCDescription: food.Description, NutritionVersion: s.Nutrition.Version,
				MatchMethod: method, Kcal: n.Kcal, ProteinG: n.ProteinG, FatG: n.FatG, CarbG: n.CarbG, FiberG: n.FiberG, SodiumMg: n.SodiumMg,
			})
		}
		// The gram-check flag stays only while the user has not changed
		// the recognized ingredients (§7.4).
		row.NeedsGramCheck = unchanged && recItem.NeedsGramCheck
		items = append(items, row)
	}
	return items, nil
}

// Create saves a confirmed recognition or a manual meal entry.
func (s *Service) Create(ctx context.Context, userID string, in Input) (store.MealEntry, error) {
	var rec *recognition.Result
	var photoID string
	source := "manual"
	if in.RecognitionID != "" {
		r, err := s.Store.GetRecognition(ctx, userID, in.RecognitionID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && r.Status != "succeeded") {
			return store.MealEntry{}, ErrRecognitionNotFound
		}
		if err != nil {
			return store.MealEntry{}, err
		}
		if r.MealEntryID != "" {
			return store.MealEntry{ID: r.MealEntryID}, store.ErrAlreadyConfirmed
		}
		var res recognition.Result
		if err := json.Unmarshal([]byte(r.ResultJSON), &res); err != nil {
			return store.MealEntry{}, err
		}
		rec, photoID, source = &res, r.PhotoID, "recognition"
	}
	items, err := s.build(ctx, in, rec)
	if err != nil {
		return store.MealEntry{}, err
	}
	id, err := s.Store.CreateMealEntry(ctx, userID, store.MealEntryInput{
		Source: source, RecognitionID: in.RecognitionID, PhotoID: photoID, MealType: in.MealType,
		EatenAt: in.EatenAt.UTC(), UTCOffset: in.UTCOffsetMinutes, EntryDate: localtime.Date(in.EatenAt, in.UTCOffsetMinutes), FoodItems: items,
	}, s.Now())
	if err != nil {
		return store.MealEntry{}, err
	}
	return s.Store.GetMealEntry(ctx, userID, id)
}

// Update replaces a meal entry. The recognition stays as it was; flags are
// compared with it by position.
func (s *Service) Update(ctx context.Context, userID, id string, in Input) (store.MealEntry, error) {
	e, err := s.Store.GetMealEntry(ctx, userID, id)
	if err != nil {
		return store.MealEntry{}, err
	}
	var rec *recognition.Result
	if e.RecognitionID != "" {
		r, err := s.Store.GetRecognition(ctx, userID, e.RecognitionID)
		if err == nil {
			var res recognition.Result
			if json.Unmarshal([]byte(r.ResultJSON), &res) == nil {
				rec = &res
			}
		}
	}
	items, err := s.build(ctx, in, rec)
	if err != nil {
		return store.MealEntry{}, err
	}
	if err := s.Store.UpdateMealEntry(ctx, userID, id, store.MealEntryInput{
		MealType: in.MealType, EatenAt: in.EatenAt.UTC(), UTCOffset: in.UTCOffsetMinutes,
		EntryDate: localtime.Date(in.EatenAt, in.UTCOffsetMinutes), FoodItems: items,
	}, s.Now()); err != nil {
		return store.MealEntry{}, err
	}
	return s.Store.GetMealEntry(ctx, userID, id)
}

// Totals sums the ingredients of entries.
func Totals(entries ...store.MealEntry) nutrition.Nutrients {
	var sum nutrition.Nutrients
	for _, e := range entries {
		for _, fi := range e.FoodItems {
			sum = sum.Add(ItemTotals(fi))
		}
	}
	return sum
}

// ItemTotals sums the ingredients of one food item.
func ItemTotals(fi store.FoodItemRow) nutrition.Nutrients {
	var sum nutrition.Nutrients
	for _, in := range fi.Ingredients {
		sum = sum.Add(IngredientNutrients(in))
	}
	return sum
}

// IngredientNutrients returns the stored snapshot as Nutrients.
func IngredientNutrients(in store.IngredientRow) nutrition.Nutrients {
	return nutrition.Nutrients{Kcal: in.Kcal, ProteinG: in.ProteinG, FatG: in.FatG, CarbG: in.CarbG, FiberG: in.FiberG, SodiumMg: in.SodiumMg}
}
