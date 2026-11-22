// Package nutritiontest builds a small nutrition.db for tests.
package nutritiontest

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wanggang316/health-assistant/server/internal/nutrition"
)

// FDC IDs in the fixture.
const (
	Tomato        = 1
	EggRaw        = 2
	EggScrambled  = 10
	RiceCooked    = 11
	VegetableOil  = 12
	BokChoyCooked = 13
)

// Aliases is the alias CSV of the fixture.
const Aliases = "alias,lang,fdc_id\n番茄,zh,1\n\"White Rice, Cooked\",en,11\n米饭,zh,11\n植物油,zh,12\nvegetable oil,en,12\n小白菜,zh,13\n"

type nut struct {
	id     int
	amount float64
}

func record(id int64, desc, cat string, kcal, p, f, c float64, extra ...nut) map[string]any {
	ns := []map[string]any{
		{"nutrient": map[string]any{"id": 1008}, "amount": kcal},
		{"nutrient": map[string]any{"id": 1003}, "amount": p},
		{"nutrient": map[string]any{"id": 1004}, "amount": f},
		{"nutrient": map[string]any{"id": 1005}, "amount": c},
	}
	for _, n := range extra {
		ns = append(ns, map[string]any{"nutrient": map[string]any{"id": n.id}, "amount": n.amount})
	}
	return map[string]any{"fdcId": id, "description": desc, "foodCategory": map[string]string{"description": cat}, "foodNutrients": ns}
}

// Build writes the fixture nutrition.db and returns its path.
func Build(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	zpath := filepath.Join(dir, "sr.zip")
	f, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("sr.json")
	json.NewEncoder(w).Encode(map[string]any{"SRLegacyFoods": []map[string]any{
		record(Tomato, "Tomatoes, red, ripe, raw", "Vegetables and Vegetable Products", 18, 0.88, 0.2, 3.89, nut{1079, 1.2}, nut{1093, 5}),
		record(EggRaw, "Egg, whole, raw, fresh", "Dairy and Egg Products", 143, 12.56, 9.51, 0.72, nut{1093, 142}),
		record(EggScrambled, "Egg, whole, cooked, scrambled", "Dairy and Egg Products", 149, 9.99, 10.98, 1.61, nut{1093, 145}),
		record(RiceCooked, "Rice, white, medium-grain, enriched, cooked", "Cereal Grains and Pasta", 130, 2.38, 0.21, 28.59, nut{1079, 0.3}, nut{1093, 0}),
		record(VegetableOil, "Oil, vegetable, soybean, refined", "Fats and Oils", 884, 0, 100, 0),
		record(BokChoyCooked, "Cabbage, chinese (pak-choi), cooked, boiled, drained, without salt", "Vegetables and Vegetable Products", 12, 1.56, 0.16, 1.78, nut{1079, 1}, nut{1093, 34}),
	}})
	zw.Close()
	f.Close()
	out := filepath.Join(dir, "nutrition.db")
	if _, err := nutrition.Build(context.Background(), out, []nutrition.Source{{Dataset: nutrition.Dataset{DataType: "sr_legacy", Tag: "test"}, ZipPath: zpath}}, strings.NewReader(Aliases)); err != nil {
		t.Fatal(err)
	}
	return out
}

// Open builds and opens the fixture.
func Open(t testing.TB) *nutrition.DB {
	t.Helper()
	db, err := nutrition.Open(Build(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
