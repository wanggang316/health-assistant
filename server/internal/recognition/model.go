// Package recognition turns a meal photo into food items with ingredients,
// matched foods, and nutrients (ADR 0002, 0003, 0009; §6).
package recognition

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PromptVersion is stored with every recognition.
const PromptVersion = "rec-v1"

// CookingMethods are the allowed cooking methods.
var CookingMethods = []string{"raw", "boiled", "steamed", "stir_fried", "pan_fried", "deep_fried", "braised", "stewed", "roasted", "grilled", "baked", "other"}

var oilMethods = map[string]bool{"stir_fried": true, "pan_fried": true, "deep_fried": true}

// ModelIngredient is one ingredient in the model output.
type ModelIngredient struct {
	NameZh       string  `json:"name_zh"`
	NameEn       string  `json:"name_en"`
	Grams        float64 `json:"grams"`
	IsCookingOil bool    `json:"is_cooking_oil"`
}

// ModelFoodItem is one food item in the model output.
type ModelFoodItem struct {
	NameZh        string            `json:"name_zh"`
	NameEn        string            `json:"name_en"`
	CookingMethod string            `json:"cooking_method"`
	Ingredients   []ModelIngredient `json:"ingredients"`
	EstimatedKcal float64           `json:"estimated_kcal"`
	Confidence    float64           `json:"confidence"`
}

// ModelOutput is the validated model output (the original recognition).
type ModelOutput struct {
	IsFood    bool            `json:"is_food"`
	FoodItems []ModelFoodItem `json:"food_items"`
}

// SystemPrompt is the system instruction (§6.3).
const SystemPrompt = `You identify foods in one meal photo for a diet log. You do not give health advice.

Rules:
1. If the photo shows no food or drink, set is_food to false and return no food items.
2. List each food item (dish, drink, side) that the person eats. Merge identical portions. Split shared plates only if the portions are clear.
3. Split each food item into ingredients with grams as served (cooked weight for cooked parts). Describe the state in name_en so that a nutrition database can match it, for example "white rice, cooked", "pork belly, braised", "egg, scrambled".
4. Cooking oil: for stir_fried, pan_fried, or deep_fried food items, and for food dressed with oil, add the oil as its own ingredient with is_cooking_oil true and an estimate of the absorbed grams. Add exactly one oil ingredient per such food item. Also add visible sauces and sugar as ingredients.
5. Estimate grams from visible cues: plate and bowl size, utensils, hands. Large portions are often underestimated; do not shrink them.
6. Give estimated_kcal per food item from your own knowledge.
7. Give confidence from 0 to 1 per food item for the identification.
8. Use Simplified Chinese for name_zh and English for name_en.`

// Schema is the response schema (OpenAPI subset, §6.4). It has no
// minItems/maxItems: the express endpoint rejected this schema with them
// (400 INVALID_ARGUMENT, 2026-11-22). Parse checks the counts.
func Schema() map[string]any {
	str := map[string]any{"type": "string"}
	ingredient := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name_zh":        str,
			"name_en":        str,
			"grams":          map[string]any{"type": "number", "minimum": 0.5, "maximum": 2000},
			"is_cooking_oil": map[string]any{"type": "boolean"},
		},
		"required":         []string{"name_zh", "name_en", "grams", "is_cooking_oil"},
		"propertyOrdering": []string{"name_zh", "name_en", "grams", "is_cooking_oil"},
	}
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name_zh":        str,
			"name_en":        str,
			"cooking_method": map[string]any{"type": "string", "enum": CookingMethods},
			"confidence":     map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"estimated_kcal": map[string]any{"type": "number", "minimum": 0, "maximum": 5000},
			"ingredients":    map[string]any{"type": "array", "items": ingredient},
		},
		"required":         []string{"name_zh", "name_en", "cooking_method", "confidence", "estimated_kcal", "ingredients"},
		"propertyOrdering": []string{"name_zh", "name_en", "cooking_method", "ingredients", "estimated_kcal", "confidence"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"is_food":    map[string]any{"type": "boolean"},
			"food_items": map[string]any{"type": "array", "items": item},
		},
		"required":         []string{"is_food", "food_items"},
		"propertyOrdering": []string{"is_food", "food_items"},
	}
}

// ValidationError lists the problems of a model output. OilOnly is true when
// the only problems are cooking-oil rules, which are accepted on the second
// attempt (§6.4).
type ValidationError struct {
	Problems []string
	OilOnly  bool
}

func (e *ValidationError) Error() string {
	return "model output invalid: " + strings.Join(e.Problems, "; ")
}

// Parse decodes and validates the model output (§6.4).
func Parse(text string) (ModelOutput, error) {
	var out ModelOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return out, &ValidationError{Problems: []string{"output is not valid JSON: " + err.Error()}}
	}
	var problems []string
	oilProblems := 0
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	switch {
	case !out.IsFood && len(out.FoodItems) > 0:
		add("is_food is false but food_items is not empty")
	case out.IsFood && len(out.FoodItems) == 0:
		add("is_food is true but food_items is empty")
	case len(out.FoodItems) > 12:
		add("more than 12 food items")
	}
	methods := map[string]bool{}
	for _, m := range CookingMethods {
		methods[m] = true
	}
	for i := range out.FoodItems {
		fi := &out.FoodItems[i]
		fi.NameZh, fi.NameEn = strings.TrimSpace(fi.NameZh), strings.TrimSpace(fi.NameEn)
		if fi.NameZh == "" || fi.NameEn == "" {
			add("food_items[%d]: empty name", i)
		}
		if !methods[fi.CookingMethod] {
			add("food_items[%d]: unknown cooking_method %q", i, fi.CookingMethod)
		}
		if fi.Confidence < 0 || fi.Confidence > 1 {
			add("food_items[%d]: confidence out of range", i)
		}
		if fi.EstimatedKcal < 0 || fi.EstimatedKcal > 5000 {
			add("food_items[%d]: estimated_kcal out of range", i)
		}
		if len(fi.Ingredients) < 1 || len(fi.Ingredients) > 15 {
			add("food_items[%d]: needs 1 to 15 ingredients", i)
		}
		sum, oils := 0.0, 0
		for j := range fi.Ingredients {
			in := &fi.Ingredients[j]
			in.NameZh, in.NameEn = strings.TrimSpace(in.NameZh), strings.TrimSpace(in.NameEn)
			if in.NameZh == "" || in.NameEn == "" {
				add("food_items[%d].ingredients[%d]: empty name", i, j)
			}
			if in.Grams < 0.5 || in.Grams > 2000 {
				add("food_items[%d].ingredients[%d]: grams %v out of range 0.5-2000", i, j, in.Grams)
			}
			sum += in.Grams
			if in.IsCookingOil {
				oils++
			}
		}
		if sum > 3000 {
			add("food_items[%d]: total grams %v is more than 3000", i, sum)
		}
		if oilMethods[fi.CookingMethod] && oils != 1 {
			add("food_items[%d]: %s needs exactly one ingredient with is_cooking_oil true, got %d", i, fi.CookingMethod, oils)
			oilProblems++
		}
	}
	if len(problems) > 0 {
		return out, &ValidationError{Problems: problems, OilOnly: oilProblems == len(problems)}
	}
	return out, nil
}
