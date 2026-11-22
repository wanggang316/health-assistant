package advice

import (
	"fmt"
	"strings"
)

func has(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// dailyTemplate is the fallback daily summary, built only from the facts.
func dailyTemplate(f DailyFacts, locale string) string {
	zh := locale == "zh-Hans"
	var parts []string
	switch {
	case f.PctOfTarget == nil && zh:
		parts = append(parts, fmt.Sprintf("今天记录了 %d 餐，共约 %d 千卡。", f.MealCount, f.Eaten.Kcal))
	case f.PctOfTarget == nil:
		parts = append(parts, fmt.Sprintf("You logged %d meals today, about %d kcal.", f.MealCount, f.Eaten.Kcal))
	case zh:
		parts = append(parts, fmt.Sprintf("今天记录了 %d 餐，约 %d 千卡，是目标的 %d%%。", f.MealCount, f.Eaten.Kcal, f.PctOfTarget.Kcal))
	default:
		parts = append(parts, fmt.Sprintf("You logged %d meals today, about %d kcal, %d%% of your target.", f.MealCount, f.Eaten.Kcal, f.PctOfTarget.Kcal))
	}
	tip := map[string][2]string{
		"kcal_over":   {"明天可以把主食或油脂减少一些。", "Tomorrow, try a little less staple food or oil."},
		"fat_high":    {"油脂偏多，可以多选蒸、煮的做法。", "Fat is high; steamed or boiled dishes can help."},
		"protein_low": {"蛋白质偏少，可以加一份鸡蛋、豆腐或瘦肉。", "Protein is low; add eggs, tofu, or lean meat."},
		"fiber_low":   {"膳食纤维偏少，多吃些蔬菜和粗粮。", "Fiber is low; add vegetables and whole grains."},
		"sodium_high": {"钠偏多，少放些酱油和盐。", "Sodium is high; use less soy sauce and salt."},
		"kcal_under":  {"吃得偏少，记得按时吃饭。", "You ate little today; keep regular meals."},
	}
	for _, flag := range []string{"kcal_over", "fat_high", "protein_low", "fiber_low", "sodium_high", "kcal_under"} {
		if has(f.Flags, flag) {
			if zh {
				parts = append(parts, tip[flag][0])
			} else {
				parts = append(parts, tip[flag][1])
			}
			break
		}
	}
	if len(parts) == 1 {
		if zh {
			parts = append(parts, "整体比较均衡，继续保持。")
		} else {
			parts = append(parts, "The day looks balanced. Keep it up.")
		}
	}
	sep := " "
	if zh {
		sep = ""
	}
	return strings.Join(parts, sep)
}

// mealTemplate is the fallback meal feedback.
func mealTemplate(f MealFacts, locale string) string {
	zh := locale == "zh-Hans"
	var first string
	switch {
	case zh:
		first = fmt.Sprintf("这一餐约 %d 千卡。", f.Eaten.Kcal)
	default:
		first = fmt.Sprintf("This meal is about %d kcal.", f.Eaten.Kcal)
	}
	var tip string
	switch {
	case has(f.Flags, "fat_share_high"):
		tip = map[bool]string{true: "油脂占比偏高，下一餐可以清淡一些。", false: "A large share of energy is from fat; keep the next meal light."}[zh]
	case has(f.Flags, "protein_share_low"):
		tip = map[bool]string{true: "蛋白质偏少，可以加一份蛋、豆制品或瘦肉。", false: "Protein is low; add eggs, soy foods, or lean meat."}[zh]
	case has(f.Flags, "no_vegetable_or_fruit"):
		tip = map[bool]string{true: "下一餐加一份蔬菜或水果会更均衡。", false: "Add vegetables or fruit to the next meal for balance."}[zh]
	default:
		tip = map[bool]string{true: "搭配不错，继续保持。", false: "A good mix. Keep it up."}[zh]
	}
	if zh {
		return first + tip
	}
	return first + " " + tip
}
