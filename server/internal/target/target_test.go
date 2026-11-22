package target

import "testing"

func TestCalculate(t *testing.T) {
	base := Profile{Sex: "male", Age: 31, HeightCm: 170, WeightKg: 61, ActivityLevel: "light", Goal: "lose", ProteinPct: 25, FatPct: 30, CarbPct: 45}
	tests := []struct {
		name string
		edit func(*Profile)
		want Daily
		ok   bool
	}{
		// BMR = 610 + 1062.5 - 155 + 5 = 1522.5; x1.375 x0.85 = 1779.4
		{"prototype default", func(*Profile) {}, Daily{Kcal: 1779, ProteinG: 111, FatG: 59, CarbG: 200}, true},
		// BMR = 1522.5 - 166 = 1356.5; x1.2 x1.0 = 1627.8
		{"female sedentary keep", func(p *Profile) { p.Sex, p.ActivityLevel, p.Goal = "female", "sedentary", "keep" }, Daily{Kcal: 1628, ProteinG: 102, FatG: 54, CarbG: 183}, true},
		{"age 18 gets a target", func(p *Profile) { p.Age = 18 }, Daily{Kcal: 1855, ProteinG: 116, FatG: 62, CarbG: 209}, true},
		{"age 17 gets none", func(p *Profile) { p.Age = 17 }, Daily{}, false},
		{"unknown activity", func(p *Profile) { p.ActivityLevel = "x" }, Daily{}, false},
		{"unknown goal", func(p *Profile) { p.Goal = "x" }, Daily{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.edit(&p)
			got, ok := Calculate(p)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("Calculate = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
