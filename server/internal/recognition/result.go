package recognition

import (
	"context"

	"github.com/wanggang316/health-assistant/server/internal/nutrition"
)

// IngredientResult is a matched ingredient. Food and Nutrients are nil when
// the ingredient is unresolved; then Candidates has foods to choose from.
type IngredientResult struct {
	NameZh       string               `json:"name_zh"`
	NameEn       string               `json:"name_en"`
	Grams        float64              `json:"grams"`
	IsCookingOil bool                 `json:"is_cooking_oil"`
	Food         *nutrition.Ref       `json:"food"`
	Nutrients    *nutrition.Nutrients `json:"nutrients"`
	MatchMethod  *string              `json:"match_method"`
	Candidates   []nutrition.Ref      `json:"candidates"`
}

// FoodItemResult is a food item with matched ingredients.
type FoodItemResult struct {
	NameZh         string              `json:"name_zh"`
	NameEn         string              `json:"name_en"`
	CookingMethod  string              `json:"cooking_method"`
	Confidence     float64             `json:"confidence"`
	ModelKcal      float64             `json:"model_kcal"`
	NeedsGramCheck bool                `json:"needs_gram_check"`
	Ingredients    []IngredientResult  `json:"ingredients"`
	Nutrients      nutrition.Nutrients `json:"nutrients"`
}

// Result is the normalized recognition (stored as result_json).
type Result struct {
	IsFood            bool                `json:"is_food"`
	SuggestedMealType string              `json:"suggested_meal_type"`
	FoodItems         []FoodItemResult    `json:"food_items"`
	Nutrients         nutrition.Nutrients `json:"nutrients"`
}

// BuildResult matches every ingredient and calculates nutrients and the
// cross-check (§7.2–7.4). Nutrients of a food item sum only resolved
// ingredients.
func BuildResult(ctx context.Context, m *nutrition.Matcher, out ModelOutput, mealType string) (Result, error) {
	var qs []nutrition.Query
	for _, fi := range out.FoodItems {
		for _, in := range fi.Ingredients {
			qs = append(qs, nutrition.Query{NameZh: in.NameZh, NameEn: in.NameEn})
		}
	}
	matches, err := m.Resolve(ctx, qs)
	if err != nil {
		return Result{}, err
	}
	res := Result{IsFood: out.IsFood, SuggestedMealType: mealType, FoodItems: []FoodItemResult{}}
	k := 0
	for _, fi := range out.FoodItems {
		item := FoodItemResult{
			NameZh: fi.NameZh, NameEn: fi.NameEn, CookingMethod: fi.CookingMethod,
			Confidence: fi.Confidence, ModelKcal: fi.EstimatedKcal,
		}
		resolved := true
		var sum nutrition.Nutrients
		for _, in := range fi.Ingredients {
			mt := matches[k]
			k++
			ir := IngredientResult{NameZh: in.NameZh, NameEn: in.NameEn, Grams: in.Grams, IsCookingOil: in.IsCookingOil, Candidates: []nutrition.Ref{}}
			if mt.Food != nil {
				ref := mt.Food.Ref()
				n := mt.Food.Per100g.ForGrams(in.Grams)
				sum = sum.Add(n)
				rn := n.Rounded()
				method := mt.Method
				ir.Food, ir.Nutrients, ir.MatchMethod = &ref, &rn, &method
			} else {
				resolved = false
				for _, c := range mt.Candidates {
					ir.Candidates = append(ir.Candidates, c.Ref())
				}
			}
			item.Ingredients = append(item.Ingredients, ir)
		}
		item.NeedsGramCheck = resolved && nutrition.NeedsGramCheck(sum.Kcal, fi.EstimatedKcal)
		item.Nutrients = sum.Rounded()
		res.Nutrients = res.Nutrients.Add(sum)
		res.FoodItems = append(res.FoodItems, item)
	}
	res.Nutrients = res.Nutrients.Rounded()
	return res, nil
}
