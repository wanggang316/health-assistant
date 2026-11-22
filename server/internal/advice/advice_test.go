package advice

import (
	"strings"
	"testing"

	"github.com/wanggang316/health-assistant/server/internal/store"
	"github.com/wanggang316/health-assistant/server/internal/target"
)

func f(v float64) *float64 { return &v }

func entry(mealType string, kcal, p, fat, c, fiber, sodium float64, fdc int64) store.MealEntry {
	return store.MealEntry{MealType: mealType, FoodItems: []store.FoodItemRow{{NameZh: "菜" + mealType, NameEn: "dish " + mealType,
		Ingredients: []store.IngredientRow{{FDCID: fdc, Kcal: kcal, ProteinG: p, FatG: fat, CarbG: c, FiberG: f(fiber), SodiumMg: f(sodium)}}}}}
}

func TestDailyFlags(t *testing.T) {
	tgt := &target.Daily{Kcal: 2000, ProteinG: 125, FatG: 67, CarbG: 225}
	tests := []struct {
		name    string
		entries []store.MealEntry
		tgt     *target.Daily
		want    []string
	}{
		{"balanced", []store.MealEntry{entry("lunch", 1000, 60, 33, 110, 10, 1000, 1), entry("dinner", 1000, 60, 33, 110, 10, 1000, 1)}, tgt, nil},
		{"over and fatty", []store.MealEntry{entry("lunch", 2400, 60, 95, 260, 5, 3900, 1)}, tgt, []string{"kcal_over", "protein_low", "fat_high", "fiber_low", "sodium_high"}},
		{"under needs two meals", []store.MealEntry{entry("lunch", 500, 30, 15, 60, 20, 100, 1)}, tgt, []string{"protein_low", "fat_low", "carb_low"}},
		{"under with two meals", []store.MealEntry{entry("lunch", 500, 30, 15, 60, 10, 100, 1), entry("snack", 500, 60, 30, 60, 10, 100, 1)}, tgt, []string{"kcal_under", "fat_low", "carb_low"}},
		{"no target", []store.MealEntry{entry("lunch", 500, 30, 15, 60, 5, 100, 1)}, nil, []string{"fiber_low"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := BuildDailyFacts("2026-11-22", tt.entries, tt.tgt, "zh-Hans")
			if strings.Join(facts.Flags, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("flags = %v, want %v", facts.Flags, tt.want)
			}
		})
	}
}

func TestMealFacts(t *testing.T) {
	e := entry("lunch", 600, 15, 40, 50, 2, 500, 7)
	facts := BuildMealFacts(e, &target.Daily{Kcal: 2000}, func(id int64) bool { return id == 8 }, "en")
	if facts.FatSharePct != 58 || facts.ProteinSharePct != 10 || *facts.PctOfDailyKcal != 30 || facts.HasProduce {
		t.Fatalf("facts = %+v", facts)
	}
	if strings.Join(facts.Flags, ",") != "fat_share_high,protein_share_low,no_vegetable_or_fruit" {
		t.Fatalf("flags = %v", facts.Flags)
	}
	if got := mealTemplate(facts, "zh-Hans"); !strings.Contains(got, "600") || !strings.Contains(got, "油脂") {
		t.Fatalf("template = %q", got)
	}
}

func TestCheckText(t *testing.T) {
	facts := `{"eaten":{"kcal":2310,"fat_g":95.4},"pct_of_target":{"kcal":114}}`
	tests := []struct {
		text, locale, want string
	}{
		{"今天吃了约 2310 千卡，是目标的 114%。", "zh-Hans", ""},
		{"脂肪约 95 克。", "zh-Hans", ""},
		{"建议每天走 8000 步。", "zh-Hans", "number not in facts: 8000"},
		{"注意预防糖尿病。", "zh-Hans", "medical word: 糖尿病"},
		{"Consider a supplement.", "en", "medical word: supplement"},
		{strings.Repeat("好", 161), "zh-Hans", "too long"},
		{"", "en", "empty"},
	}
	for _, tt := range tests {
		if got := CheckText(tt.text, facts, tt.locale); got != tt.want {
			t.Errorf("CheckText(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestDailyTemplateUsesOnlyFacts(t *testing.T) {
	facts := BuildDailyFacts("2026-11-22", []store.MealEntry{entry("lunch", 2400, 60, 95, 260, 5, 3900, 1)},
		&target.Daily{Kcal: 2000, ProteinG: 125, FatG: 67, CarbG: 225}, "en")
	text := dailyTemplate(facts, "en")
	b := `{"meal_count":1,"kcal":2400,"pct":120}`
	if p := CheckText(text, b, "en"); p != "" {
		t.Fatalf("template %q: %s", text, p)
	}
}
