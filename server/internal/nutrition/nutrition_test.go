package nutrition

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nut struct {
	id     int
	amount float64
}

func fdcRecord(id int64, desc, cat string, nuts ...nut) map[string]any {
	var ns []map[string]any
	for _, n := range nuts {
		ns = append(ns, map[string]any{"nutrient": map[string]any{"id": n.id}, "amount": n.amount})
	}
	return map[string]any{"fdcId": id, "description": desc, "foodCategory": map[string]string{"description": cat}, "foodNutrients": ns}
}

func writeZip(t *testing.T, dir, name, key string, foods []map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, name+".zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(name + ".json")
	json.NewEncoder(w).Encode(map[string]any{key: foods})
	zw.Close()
	f.Close()
	return path
}

// buildFixture writes a small nutrition.db.
func buildFixture(t *testing.T, aliases string) (*DB, error) {
	t.Helper()
	dir := t.TempDir()
	macros := func(p, f, c float64) []nut { return []nut{{nutProtein, p}, {nutFat, f}, {nutCarb, c}} }
	fnd := writeZip(t, dir, "fnd", "FoundationFoods", []map[string]any{
		fdcRecord(1, "Tomatoes, red, ripe, raw", "Vegetables and Vegetable Products", append(macros(0.9, 0.2, 3.9), nut{nutEnergyAtwaterG, 18}, nut{nutFiber, 1.2}, nut{nutSodium, 5})...),
		fdcRecord(2, "Egg, whole, raw", "Dairy and Egg Products", append(macros(12.4, 9.96, 0.96), nut{nutEnergyAtwaterS, 143}, nut{nutEnergyAtwaterG, 150})...),
		fdcRecord(3, "No macros", "x", nut{nutEnergyKcal, 1}),
	})
	sr := writeZip(t, dir, "sr", "SRLegacyFoods", []map[string]any{
		fdcRecord(10, "Egg, whole, cooked, scrambled", "Dairy and Egg Products", append(macros(10, 11, 1.6), nut{nutEnergyKcal, 149})...),
		fdcRecord(11, "Rice, white, medium-grain, enriched, cooked", "Cereal Grains and Pasta", append(macros(2.4, 0.2, 28.6), nut{nutEnergyKcal, 130})...),
		fdcRecord(12, "Oil, vegetable, soybean, refined", "Fats and Oils", macros(0, 100, 0)...),
		fdcRecord(2, "Duplicate id loses to foundation", "x", macros(1, 1, 1)...),
	})
	out := filepath.Join(dir, "nutrition.db")
	_, err := Build(context.Background(), out, []Source{
		{Dataset: Dataset{DataType: "foundation", Tag: "fnd-x"}, ZipPath: fnd},
		{Dataset: Dataset{DataType: "sr_legacy", Tag: "sr-x"}, ZipPath: sr},
	}, strings.NewReader(aliases))
	if err != nil {
		return nil, err
	}
	db, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, nil
}

const testAliases = "alias,lang,fdc_id\n番茄,zh,1\n\"White Rice, Cooked\",en,11\n植物油,zh,12\n"

func TestBuildImportsFoodsAndAliases(t *testing.T) {
	db, err := buildFixture(t, testAliases)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if db.Version != "fnd-x+sr-x" {
		t.Fatalf("version = %q", db.Version)
	}
	tom, err := db.Food(ctx, 1)
	if err != nil || tom.Per100g.Kcal != 18 || *tom.Per100g.FiberG != 1.2 || !tom.IsProduce() {
		t.Fatalf("tomato = %+v, %v", tom, err)
	}
	egg, _ := db.Food(ctx, 2)
	if egg.Per100g.Kcal != 143 || egg.Description != "Egg, whole, raw" {
		t.Fatalf("egg: Atwater specific before general, first dataset wins: %+v", egg)
	}
	oil, _ := db.Food(ctx, 12)
	if oil.Per100g.Kcal != 900 || oil.Per100g.FiberG != nil {
		t.Fatalf("oil: energy from 4/9/4: %+v", oil)
	}
	if _, err := db.Food(ctx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("food without macros: %v", err)
	}
	f, ok, err := db.Alias(ctx, " white  rice, COOKED ", "en")
	if err != nil || !ok || f.FDCID != 11 {
		t.Fatalf("alias en = %+v %v %v", f, ok, err)
	}
}

func TestBuildRejectsUnknownAliasTarget(t *testing.T) {
	_, err := buildFixture(t, "alias,lang,fdc_id\nx,en,999\n")
	if err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v", err)
	}
}

func TestCandidatesPreferStateAndCoverage(t *testing.T) {
	db, err := buildFixture(t, testAliases)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.Candidates(context.Background(), "egg, scrambled", 5)
	if err != nil || len(c) == 0 || c[0].FDCID != 10 {
		t.Fatalf("candidates = %+v, %v", c, err)
	}
	s, err := db.Search(context.Background(), "番", 5)
	if err != nil || len(s) != 1 || s[0].FDCID != 1 {
		t.Fatalf("search zh prefix = %+v, %v", s, err)
	}
}

type memCache map[string]int64

func (m memCache) CachedMatch(_ context.Context, k, _ string) (int64, bool, error) {
	id, ok := m[k]
	return id, ok, nil
}
func (m memCache) SaveMatch(_ context.Context, k string, id int64, _ string) error {
	m[k] = id
	return nil
}

type fakePicker struct {
	picks []int64
	err   error
	got   []PickItem
}

func (p *fakePicker) Pick(_ context.Context, items []PickItem) ([]int64, error) {
	p.got = items
	return p.picks, p.err
}

func TestMatcherResolve(t *testing.T) {
	db, err := buildFixture(t, testAliases)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	qs := []Query{
		{NameZh: "番茄", NameEn: "tomato"},             // alias zh
		{NameZh: "米饭", NameEn: "white rice, cooked"}, // alias en
		{NameZh: "炒蛋", NameEn: "egg, scrambled"},     // model pick
		{NameZh: "龙肉", NameEn: "dragon meat"},        // no candidates
	}
	cache := memCache{}
	p := &fakePicker{picks: []int64{10}}
	m := &Matcher{DB: db, Cache: cache, Picker: p, Log: log}
	got, err := m.Resolve(ctx, qs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{MethodAlias, MethodAlias, MethodModel, ""}
	for i, w := range want {
		if got[i].Method != w {
			t.Errorf("query %d method = %q, want %q", i, got[i].Method, w)
		}
	}
	if got[2].Food.FDCID != 10 || cache["egg scrambled"] != 10 || len(p.got) != 1 {
		t.Fatalf("pick: %+v cache %v items %d", got[2], cache, len(p.got))
	}
	if got[3].Food != nil || len(got[3].Candidates) != 0 {
		t.Fatalf("unknown: %+v", got[3])
	}

	// The cached pick is used next time, without a model call.
	p2 := &fakePicker{}
	got, _ = (&Matcher{DB: db, Cache: cache, Picker: p2, Log: log}).Resolve(ctx, qs[2:3])
	if got[0].Method != MethodCache || p2.got != nil {
		t.Fatalf("cache: %+v", got[0])
	}

	// "none" leaves it unresolved with candidates; a failed pick falls back to FTS.
	got, _ = (&Matcher{DB: db, Cache: memCache{}, Picker: &fakePicker{picks: []int64{0}}, Log: log}).Resolve(ctx, qs[2:3])
	if got[0].Food != nil || len(got[0].Candidates) == 0 {
		t.Fatalf("none: %+v", got[0])
	}
	got, _ = (&Matcher{DB: db, Cache: memCache{}, Picker: &fakePicker{err: errors.New("down")}, Log: log}).Resolve(ctx, qs[2:3])
	if got[0].Method != MethodFTS || got[0].Food.FDCID != 10 {
		t.Fatalf("pick error: %+v", got[0])
	}
}

func TestNutrientMath(t *testing.T) {
	fiber := 1.2
	per := Nutrients{Kcal: 18, ProteinG: 0.88, FatG: 0.2, CarbG: 3.89, FiberG: &fiber}
	n := per.ForGrams(150)
	if r := n.Rounded(); r.Kcal != 27 || *r.FiberG != 1.8 || r.ProteinG != 1.3 || n.SodiumMg != nil {
		t.Fatalf("ForGrams = %+v", n)
	}
	sodium := 10.0
	sum := n.Add(Nutrients{Kcal: 1, SodiumMg: &sodium}).Rounded()
	if sum.Kcal != 28 || sum.ProteinG != 1.3 || *sum.SodiumMg != 10 || *sum.FiberG != 1.8 {
		t.Fatalf("Add/Rounded = %+v", sum)
	}
}

func TestNeedsGramCheck(t *testing.T) {
	tests := []struct {
		db, model float64
		want      bool
	}{
		{100, 135, false}, {100, 136, true}, {100, 64, true}, {29, 100, false}, {263, 260, false},
	}
	for _, tt := range tests {
		if got := NeedsGramCheck(tt.db, tt.model); got != tt.want {
			t.Errorf("NeedsGramCheck(%v, %v) = %v", tt.db, tt.model, got)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{" Egg,  Scrambled ": "egg scrambled", "ＡＢＣ（大米）": "abc 大米", "番茄!": "番茄"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRealDatabase checks common names against the real nutrition.db when
// NUTRITION_DB points to it (CI builds it).
func TestRealDatabase(t *testing.T) {
	path := os.Getenv("NUTRITION_DB")
	if path == "" {
		t.Skip("NUTRITION_DB not set")
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, zh := range []string{"米饭", "鸡蛋", "番茄", "植物油", "小白菜", "猪肉", "豆腐", "牛奶", "苹果", "酱油"} {
		if _, ok, err := db.Alias(ctx, zh, "zh"); err != nil || !ok {
			t.Errorf("alias %s: %v %v", zh, ok, err)
		}
	}
	for _, en := range []string{"chicken breast, roasted", "broccoli, boiled", "salmon, cooked"} {
		c, err := db.Candidates(ctx, en, 3)
		if err != nil || len(c) == 0 {
			t.Errorf("candidates %s: %v %v", en, c, err)
			continue
		}
		t.Logf("%s -> %s", en, c[0].Description)
	}
}
