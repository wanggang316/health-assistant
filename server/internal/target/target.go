// Package target calculates the daily target from a profile (ADR 0010).
package target

import "math"

// Profile is the input of the calculation.
type Profile struct {
	Sex           string // "male" or "female"
	Age           int
	HeightCm      float64
	WeightKg      float64
	ActivityLevel string
	Goal          string
	ProteinPct    int
	FatPct        int
	CarbPct       int
}

// Daily is a daily target.
type Daily struct {
	Kcal     int `json:"kcal"`
	ProteinG int `json:"protein_g"`
	FatG     int `json:"fat_g"`
	CarbG    int `json:"carb_g"`
}

// ActivityFactors maps activity levels to Mifflin-St Jeor multipliers.
var ActivityFactors = map[string]float64{
	"sedentary":   1.2,
	"light":       1.375,
	"moderate":    1.55,
	"active":      1.725,
	"very_active": 1.9,
}

// GoalFactors maps goals to energy multipliers.
var GoalFactors = map[string]float64{
	"lose": 0.85,
	"keep": 1.0,
	"gain": 1.10,
}

// MinAge is the lowest age that gets a daily target.
const MinAge = 18

// Calculate returns the daily target, or false if the profile gets none
// (age under 18, or unknown activity level or goal).
func Calculate(p Profile) (Daily, bool) {
	af, okA := ActivityFactors[p.ActivityLevel]
	gf, okG := GoalFactors[p.Goal]
	if p.Age < MinAge || !okA || !okG {
		return Daily{}, false
	}
	bmr := 10*p.WeightKg + 6.25*p.HeightCm - 5*float64(p.Age)
	if p.Sex == "male" {
		bmr += 5
	} else {
		bmr -= 161
	}
	kcal := math.Round(bmr * af * gf)
	return Daily{
		Kcal:     int(kcal),
		ProteinG: int(math.Round(kcal * float64(p.ProteinPct) / 400)),
		FatG:     int(math.Round(kcal * float64(p.FatPct) / 900)),
		CarbG:    int(math.Round(kcal * float64(p.CarbPct) / 400)),
	}, true
}
