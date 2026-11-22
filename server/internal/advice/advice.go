// Package advice builds health advice: rules calculate the facts, the model
// only writes the text (ADR 0005), and the text is cached until the facts
// change (ADR 0012).
package advice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/meals"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/store"
	"github.com/wanggang316/health-assistant/server/internal/target"
)

// Kinds of advice.
const (
	KindMealFeedback = "meal_feedback"
	KindDailySummary = "daily_summary"
)

// Rule thresholds (§8.2). One table, unit-tested.
var Thresholds = struct {
	KcalOver, KcalUnder, MacroHigh, MacroLow, FiberLowG, SodiumHighMg, FatShareHigh, ProteinShareLow float64
	KcalUnderMinMeals                                                                                int
}{
	KcalOver: 110, KcalUnder: 80, MacroHigh: 120, MacroLow: 70, FiberLowG: 15, SodiumHighMg: 2300,
	FatShareHigh: 40, ProteinShareLow: 15, KcalUnderMinMeals: 2,
}

// Macros are rounded grams and energy.
type Macros struct {
	Kcal     int `json:"kcal"`
	ProteinG int `json:"protein_g"`
	FatG     int `json:"fat_g"`
	CarbG    int `json:"carb_g"`
}

// Eaten adds fiber and sodium.
type Eaten struct {
	Macros
	FiberG   int `json:"fiber_g"`
	SodiumMg int `json:"sodium_mg"`
}

// Item is a named energy value.
type Item struct {
	Name string `json:"name"`
	Kcal int    `json:"kcal"`
}

// DailyFacts are the facts of a daily summary.
type DailyFacts struct {
	Date         string   `json:"date"`
	HasTarget    bool     `json:"has_target"`
	MealCount    int      `json:"meal_count"`
	MealTypes    []string `json:"meal_types"`
	Eaten        Eaten    `json:"eaten"`
	Target       *Macros  `json:"target,omitempty"`
	PctOfTarget  *Macros  `json:"pct_of_target,omitempty"`
	Flags        []string `json:"flags"`
	TopKcalItems []Item   `json:"top_kcal_items"`
}

// MealFacts are the facts of a meal feedback.
type MealFacts struct {
	MealType        string   `json:"meal_type"`
	Items           []Item   `json:"items"`
	Eaten           Eaten    `json:"eaten"`
	PctOfDailyKcal  *int     `json:"pct_of_daily_kcal,omitempty"`
	ProteinSharePct int      `json:"protein_energy_share_pct"`
	FatSharePct     int      `json:"fat_energy_share_pct"`
	CarbSharePct    int      `json:"carb_energy_share_pct"`
	HasProduce      bool     `json:"has_vegetable_or_fruit"`
	Flags           []string `json:"flags"`
}

func r(v float64) int { return int(math.Round(v)) }

func eaten(n nutrition.Nutrients) Eaten {
	e := Eaten{Macros: Macros{Kcal: r(n.Kcal), ProteinG: r(n.ProteinG), FatG: r(n.FatG), CarbG: r(n.CarbG)}}
	if n.FiberG != nil {
		e.FiberG = r(*n.FiberG)
	}
	if n.SodiumMg != nil {
		e.SodiumMg = r(*n.SodiumMg)
	}
	return e
}

func pct(a, b int) int {
	if b <= 0 {
		return 0
	}
	return r(float64(a) * 100 / float64(b))
}

func itemName(fi store.FoodItemRow, locale string) string {
	if locale == "zh-Hans" && fi.NameZh != "" {
		return fi.NameZh
	}
	if fi.NameEn != "" {
		return fi.NameEn
	}
	return fi.NameZh
}

// BuildDailyFacts applies the daily rules (§8.2).
func BuildDailyFacts(date string, entries []store.MealEntry, tgt *target.Daily, locale string) DailyFacts {
	f := DailyFacts{Date: date, MealCount: len(entries), MealTypes: []string{}, Flags: []string{}, TopKcalItems: []Item{}}
	seen := map[string]bool{}
	var items []Item
	for _, e := range entries {
		if !seen[e.MealType] {
			seen[e.MealType] = true
			f.MealTypes = append(f.MealTypes, e.MealType)
		}
		for _, fi := range e.FoodItems {
			items = append(items, Item{Name: itemName(fi, locale), Kcal: r(meals.ItemTotals(fi).Kcal)})
		}
	}
	f.Eaten = eaten(meals.Totals(entries...))
	sort.SliceStable(items, func(i, j int) bool { return items[i].Kcal > items[j].Kcal })
	if len(items) > 3 {
		items = items[:3]
	}
	f.TopKcalItems = append(f.TopKcalItems, items...)
	t := Thresholds
	if tgt != nil {
		f.HasTarget = true
		f.Target = &Macros{Kcal: tgt.Kcal, ProteinG: tgt.ProteinG, FatG: tgt.FatG, CarbG: tgt.CarbG}
		p := Macros{Kcal: pct(f.Eaten.Kcal, tgt.Kcal), ProteinG: pct(f.Eaten.ProteinG, tgt.ProteinG), FatG: pct(f.Eaten.FatG, tgt.FatG), CarbG: pct(f.Eaten.CarbG, tgt.CarbG)}
		f.PctOfTarget = &p
		if float64(p.Kcal) > t.KcalOver {
			f.Flags = append(f.Flags, "kcal_over")
		}
		if float64(p.Kcal) < t.KcalUnder && f.MealCount >= t.KcalUnderMinMeals {
			f.Flags = append(f.Flags, "kcal_under")
		}
		for _, m := range []struct {
			name string
			v    int
		}{{"protein", p.ProteinG}, {"fat", p.FatG}, {"carb", p.CarbG}} {
			if float64(m.v) > t.MacroHigh {
				f.Flags = append(f.Flags, m.name+"_high")
			} else if float64(m.v) < t.MacroLow {
				f.Flags = append(f.Flags, m.name+"_low")
			}
		}
	}
	if float64(f.Eaten.FiberG) < t.FiberLowG {
		f.Flags = append(f.Flags, "fiber_low")
	}
	if float64(f.Eaten.SodiumMg) > t.SodiumHighMg {
		f.Flags = append(f.Flags, "sodium_high")
	}
	return f
}

// BuildMealFacts applies the meal feedback rules (§8.2). produce reports
// whether an FDC food is in a vegetable or fruit category.
func BuildMealFacts(e store.MealEntry, tgt *target.Daily, produce func(int64) bool, locale string) MealFacts {
	f := MealFacts{MealType: e.MealType, Items: []Item{}, Flags: []string{}}
	for _, fi := range e.FoodItems {
		f.Items = append(f.Items, Item{Name: itemName(fi, locale), Kcal: r(meals.ItemTotals(fi).Kcal)})
		for _, in := range fi.Ingredients {
			if produce(in.FDCID) {
				f.HasProduce = true
			}
		}
	}
	n := meals.Totals(e)
	f.Eaten = eaten(n)
	energy := 4*n.ProteinG + 9*n.FatG + 4*n.CarbG
	if energy > 0 {
		f.ProteinSharePct = r(4 * n.ProteinG * 100 / energy)
		f.FatSharePct = r(9 * n.FatG * 100 / energy)
		f.CarbSharePct = 100 - f.ProteinSharePct - f.FatSharePct
	}
	if tgt != nil {
		p := pct(f.Eaten.Kcal, tgt.Kcal)
		f.PctOfDailyKcal = &p
	}
	if float64(f.FatSharePct) > Thresholds.FatShareHigh {
		f.Flags = append(f.Flags, "fat_share_high")
	}
	if float64(f.ProteinSharePct) < Thresholds.ProteinShareLow {
		f.Flags = append(f.Flags, "protein_share_low")
	}
	if !f.HasProduce {
		f.Flags = append(f.Flags, "no_vegetable_or_fruit")
	}
	return f
}

func hashFacts(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Generator is the gemini client.
type Generator interface {
	Generate(ctx context.Context, r gemini.Request) (gemini.Response, error)
}

// Service makes and caches advice text.
type Service struct {
	Store     *store.Store
	Nutrition *nutrition.DB
	Gemini    Generator
	Model     string
	Log       *slog.Logger
	Now       func() time.Time

	sf singleflight.Group
}

// Text is advice text with its time.
type Text struct {
	Text        string
	GeneratedAt time.Time
}

// ErrNoEntries means the day has no meal entries.
var ErrNoEntries = errors.New("no meal entries")

func (s *Service) produce(id int64) bool {
	f, err := s.Nutrition.Food(context.Background(), id)
	return err == nil && f.IsProduce()
}

// DailyTarget returns the daily target of the user, or nil.
func (s *Service) DailyTarget(ctx context.Context, userID string) (*target.Daily, error) {
	p, err := s.Store.GetProfile(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if d, ok := target.FromProfile(p, s.Now()); ok {
		return &d, nil
	}
	return nil, nil
}

// SummaryState returns the cached daily summary state for the day view:
// none (no entries), missing, stale, or fresh (with text). A stale summary is
// never returned as text.
func (s *Service) SummaryState(ctx context.Context, userID, date, locale string, entries []store.MealEntry, tgt *target.Daily) (string, *Text, error) {
	if len(entries) == 0 {
		return "none", nil, nil
	}
	a, err := s.Store.GetAdvice(ctx, userID, KindDailySummary, "", date, locale)
	if errors.Is(err, store.ErrNotFound) {
		return "missing", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	b, _ := json.Marshal(BuildDailyFacts(date, entries, tgt, locale))
	if a.Stale || a.FactsHash != hashFacts(b) {
		return "stale", nil, nil
	}
	return "fresh", &Text{a.Text, a.GeneratedAt}, nil
}

// DailySummary returns a fresh daily summary, making it if needed.
func (s *Service) DailySummary(ctx context.Context, userID, date, locale string) (Text, error) {
	entries, err := s.Store.DayEntries(ctx, userID, date)
	if err != nil {
		return Text{}, err
	}
	if len(entries) == 0 {
		return Text{}, ErrNoEntries
	}
	tgt, err := s.DailyTarget(ctx, userID)
	if err != nil {
		return Text{}, err
	}
	facts := BuildDailyFacts(date, entries, tgt, locale)
	return s.get(ctx, store.Advice{Kind: KindDailySummary, UserID: userID, EntryDate: date, Locale: locale}, facts,
		func() string { return dailyTemplate(facts, locale) })
}

// MealFeedback returns a fresh meal feedback, making it if needed.
func (s *Service) MealFeedback(ctx context.Context, userID, entryID, locale string) (Text, error) {
	e, err := s.Store.GetMealEntry(ctx, userID, entryID)
	if err != nil {
		return Text{}, err
	}
	tgt, err := s.DailyTarget(ctx, userID)
	if err != nil {
		return Text{}, err
	}
	facts := BuildMealFacts(e, tgt, s.produce, locale)
	return s.get(ctx, store.Advice{Kind: KindMealFeedback, UserID: userID, MealEntryID: entryID, Locale: locale}, facts,
		func() string { return mealTemplate(facts, locale) })
}

func (s *Service) get(ctx context.Context, key store.Advice, facts any, template func() string) (Text, error) {
	b, _ := json.Marshal(facts)
	hash := hashFacts(b)
	cached, err := s.Store.GetAdvice(ctx, key.UserID, key.Kind, key.MealEntryID, key.EntryDate, key.Locale)
	if err == nil && !cached.Stale && cached.FactsHash == hash {
		return Text{cached.Text, cached.GeneratedAt}, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Text{}, err
	}
	sfKey := strings.Join([]string{key.Kind, key.UserID, key.MealEntryID, key.EntryDate, key.Locale, hash}, "|")
	v, err, _ := s.sf.Do(sfKey, func() (any, error) {
		text, model := s.write(context.WithoutCancel(ctx), key.Kind, string(b), key.Locale)
		if text == "" {
			text, model = template(), "template"
		}
		a := key
		a.FactsJSON, a.FactsHash, a.Text, a.Model, a.GeneratedAt = string(b), hash, text, model, s.Now()
		if err := s.Store.PutAdvice(context.WithoutCancel(ctx), a); err != nil {
			return nil, err
		}
		return Text{a.Text, a.GeneratedAt}, nil
	})
	if err != nil {
		return Text{}, err
	}
	return v.(Text), nil
}

const advicePrompt = `You write short diet feedback for a meal-logging app.
Write 2 or 3 short sentences in %s, using only the facts in the JSON.
Rules: general diet information only. Do not mention diseases, medicines, diagnoses, or supplements. Do not write numbers that are not in the facts. Be friendly and do not judge. Give one practical suggestion.`

func languageName(locale string) string {
	if locale == "zh-Hans" {
		return "Simplified Chinese"
	}
	return "English"
}

// write asks the model, checks the text, and retries once. It returns "" on
// failure, so that the caller uses the template.
func (s *Service) write(ctx context.Context, kind, factsJSON, locale string) (string, string) {
	if s.Gemini == nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := s.Gemini.Generate(ctx, gemini.Request{
			Model: s.Model, System: fmt.Sprintf(advicePrompt, languageName(locale)),
			Text:            "Kind: " + kind + "\nFacts: " + factsJSON,
			Schema:          map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}},
			MaxOutputTokens: 512, ThinkingLevel: "MINIMAL", Timeout: 8 * time.Second,
		})
		_ = s.Store.AddModelUsage(context.WithoutCancel(ctx), s.Now().UTC().Format(time.DateOnly), s.Model, "advice", 1,
			resp.Usage.InputTokens, resp.Usage.OutputTokens+resp.Usage.ThoughtTokens)
		if err != nil {
			s.Log.Warn("advice model call", "err", err)
			continue
		}
		var out struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
			continue
		}
		text := strings.TrimSpace(out.Text)
		if problem := CheckText(text, factsJSON, locale); problem != "" {
			s.Log.Warn("advice text rejected", "problem", problem)
			continue
		}
		return text, s.Model
	}
	return "", ""
}

var numberRE = regexp.MustCompile(`\d+(?:\.\d+)?`)

var denyWords = map[string][]string{
	"zh-Hans": {"糖尿病", "高血压", "疾病", "药", "诊断", "治疗", "补充剂", "保健品", "癌"},
	"en":      {"diabetes", "hypertension", "disease", "medicine", "medication", "diagnos", "treatment", "supplement", "cancer", "drug"},
}

// CheckText returns a problem, or "" if the text is usable (§8.3): length,
// every number from the facts, no medical words.
func CheckText(text, factsJSON, locale string) string {
	if text == "" {
		return "empty"
	}
	limit := 320
	if locale == "zh-Hans" {
		limit = 160
	}
	if utf8.RuneCountInString(text) > limit {
		return "too long"
	}
	allowed := map[string]bool{}
	for _, n := range numberRE.FindAllString(factsJSON, -1) {
		allowed[n] = true
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			allowed[strconv.Itoa(r(f))] = true
		}
	}
	for _, n := range numberRE.FindAllString(text, -1) {
		if !allowed[n] {
			return "number not in facts: " + n
		}
	}
	lower := strings.ToLower(text)
	for _, w := range denyWords[locale] {
		if strings.Contains(lower, w) {
			return "medical word: " + w
		}
	}
	return ""
}
