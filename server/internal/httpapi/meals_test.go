package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wanggang316/health-assistant/server/internal/nutrition/nutritiontest"
)

// confirmBody turns the recognition response into a meal entry body: the
// user picks the scrambled egg for the unresolved ingredient and changes
// the tomato to 180 g.
func confirmBody(recID, eatenAt string) map[string]any {
	return map[string]any{
		"recognition_id": recID, "meal_type": "lunch", "eaten_at": eatenAt, "utc_offset_minutes": 480,
		"food_items": []map[string]any{{
			"name_zh": "番茄炒蛋", "name_en": "Tomato and egg stir-fry", "cooking_method": "stir_fried",
			"ingredients": []map[string]any{
				{"name_zh": "番茄", "name_en": "tomato, raw", "grams": 180, "fdc_id": nutritiontest.Tomato, "is_cooking_oil": false},
				{"name_zh": "炒蛋", "name_en": "egg, scrambled", "grams": 100, "fdc_id": nutritiontest.EggScrambled, "is_cooking_oil": false},
				{"name_zh": "植物油", "name_en": "vegetable oil", "grams": 10, "fdc_id": nutritiontest.VegetableOil, "is_cooking_oil": true},
			},
		}},
	}
}

func TestMealEntryFlow(t *testing.T) {
	a := newTestAPI(t)
	a.now = time.Date(2026, 11, 22, 4, 30, 0, 0, time.UTC)
	access, _ := a.signInApple()
	a.do("PUT", "/v1/me/profile", access, map[string]any{"sex": "male", "birth_year": 1995, "height_cm": 170, "weight_kg": 61, "activity_level": "light", "goal": "lose"})

	a.gem.texts = []string{dishOutput}
	rec := a.uploadPhoto(access, uuid.NewString(), testJPEG(t), `{"taken_at":"2026-11-22T04:12:00Z","utc_offset_minutes":480}`).json(t)
	recID := rec["id"].(string)

	key := uuid.NewString()
	body := confirmBody(recID, "2026-11-22T04:12:00Z")
	r := a.do("POST", "/v1/meal-entries", access, body, "Idempotency-Key", key)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "meal_entry.json")
	e := r.json(t)
	id := e["id"].(string)
	// 180 g tomato 32.4 + 100 g egg 149 + 10 g oil 88.4 = 269.8
	if e["entry_date"] != "2026-11-22" || e["source"] != "recognition" || e["nutrients"].(map[string]any)["kcal"] != 269.8 || e["photo_url"] == nil {
		t.Fatalf("entry = %s", r.body)
	}
	ings := e["food_items"].([]any)[0].(map[string]any)["ingredients"].([]any)
	if ings[0].(map[string]any)["match_method"] != "alias" || ings[1].(map[string]any)["match_method"] != "user" {
		t.Fatalf("match methods = %v", ings)
	}

	// Replay and conflicts.
	if r := a.do("POST", "/v1/meal-entries", access, body, "Idempotency-Key", key); r.status != 201 || r.json(t)["id"] != id {
		t.Fatalf("replay: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/v1/meal-entries", access, body, "Idempotency-Key", uuid.NewString()); r.status != 409 || errorCode(t, r) != "recognition_already_confirmed" {
		t.Fatalf("second confirm: %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/v1/recognitions/"+recID, access, nil); r.json(t)["meal_entry_id"] != id {
		t.Fatalf("recognition link: %s", r.body)
	}

	// Meal feedback: the model text uses a number that is not in the facts,
	// so it is rejected twice and the template is used.
	a.adviceGem.texts = []string{`{"text":"走 10000 步吧"}`, `{"text":"走 10000 步吧"}`}
	r = a.do("GET", "/v1/meal-entries/"+id+"/feedback", access, nil, "Accept-Language", "zh-Hans")
	if r.status != 200 || !strings.Contains(r.json(t)["text"].(string), "270") {
		t.Fatalf("feedback: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "advice_meal_feedback.json")

	// Day view: summary missing, then made, then fresh, then stale after an edit.
	r = a.do("GET", "/v1/days/2026-11-22", access, nil, "Accept-Language", "zh-Hans")
	assertShape(t, r.body, "day.json")
	day := r.json(t)
	if day["daily_summary"].(map[string]any)["state"] != "missing" || len(day["meal_entries"].([]any)) != 1 || day["daily_target"] == nil {
		t.Fatalf("day: %s", r.body)
	}
	a.adviceGem.texts = []string{`{"text":"今天午餐约 270 千卡，搭配了蔬菜。"}`}
	r = a.do("GET", "/v1/days/2026-11-22/summary", access, nil, "Accept-Language", "zh-Hans")
	if r.status != 200 || r.json(t)["text"] != "今天午餐约 270 千卡，搭配了蔬菜。" {
		t.Fatalf("summary: %d %s", r.status, r.body)
	}
	assertShape(t, r.body, "advice_daily_summary.json")
	if st := a.do("GET", "/v1/days/2026-11-22", access, nil, "Accept-Language", "zh-Hans").json(t)["daily_summary"].(map[string]any); st["state"] != "fresh" || st["text"] == nil {
		t.Fatalf("fresh: %v", st)
	}
	if st := a.do("GET", "/v1/days/2026-11-22", access, nil, "Accept-Language", "en").json(t)["daily_summary"].(map[string]any); st["state"] != "missing" {
		t.Fatalf("other locale: %v", st)
	}

	body["meal_type"] = "dinner"
	delete(body, "recognition_id")
	body["food_items"].([]map[string]any)[0]["ingredients"].([]map[string]any)[0]["grams"] = 200
	r = a.do("PUT", "/v1/meal-entries/"+id, access, body)
	if r.status != 200 || r.json(t)["meal_type"] != "dinner" {
		t.Fatalf("update: %d %s", r.status, r.body)
	}
	if st := a.do("GET", "/v1/days/2026-11-22", access, nil, "Accept-Language", "zh-Hans").json(t)["daily_summary"].(map[string]any); st["state"] != "stale" || st["text"] != nil {
		t.Fatalf("stale: %v", st)
	}

	// Manual entry, list, calendar.
	manual := map[string]any{"meal_type": "breakfast", "eaten_at": "2026-11-21T23:30:00Z", "utc_offset_minutes": 480,
		"food_items": []map[string]any{{"name_zh": "米饭", "name_en": "rice", "cooking_method": "steamed",
			"ingredients": []map[string]any{{"name_zh": "米饭", "name_en": "rice", "grams": 150, "fdc_id": nutritiontest.RiceCooked}}}}}
	if r := a.do("POST", "/v1/meal-entries", access, manual, "Idempotency-Key", uuid.NewString()); r.status != 201 || r.json(t)["entry_date"] != "2026-11-22" {
		t.Fatalf("manual: %d %s", r.status, r.body)
	}
	r = a.do("GET", "/v1/meal-entries?limit=1", access, nil)
	assertShape(t, r.body, "meal_entries.json")
	page := r.json(t)
	if len(page["meal_entries"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatalf("page 1: %s", r.body)
	}
	r = a.do("GET", "/v1/meal-entries?limit=1&cursor="+page["next_cursor"].(string), access, nil)
	if p2 := r.json(t); len(p2["meal_entries"].([]any)) != 1 || p2["next_cursor"] != nil {
		t.Fatalf("page 2: %s", r.body)
	}
	r = a.do("GET", "/v1/days?from=2026-11-01&to=2026-11-30", access, nil)
	assertShape(t, r.body, "days.json")
	days := r.json(t)["days"].([]any)
	if len(days) != 1 || days[0].(map[string]any)["entry_count"] != 2.0 {
		t.Fatalf("days: %s", r.body)
	}

	// Delete removes the entry, the recognition, and the photo.
	if r := a.do("DELETE", "/v1/meal-entries/"+id, access, nil); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if a.photos.count() != 0 {
		t.Fatalf("photos = %d", a.photos.count())
	}
	if code := errorCode(t, a.do("GET", "/v1/recognitions/"+recID, access, nil)); code != "not_found" {
		t.Fatalf("recognition after delete: %s", code)
	}
}

func TestMealEntryErrors(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	ok := func() map[string]any {
		return map[string]any{"meal_type": "lunch", "eaten_at": a.now.Format(time.RFC3339), "utc_offset_minutes": 0,
			"food_items": []map[string]any{{"name_zh": "x", "cooking_method": "raw",
				"ingredients": []map[string]any{{"name_zh": "x", "grams": 10, "fdc_id": nutritiontest.Tomato}}}}}
	}
	tests := []struct {
		name   string
		edit   func(m map[string]any)
		status int
		code   string
	}{
		{"bad meal type", func(m map[string]any) { m["meal_type"] = "brunch" }, 400, "invalid_request"},
		{"no items", func(m map[string]any) { m["food_items"] = []any{} }, 400, "invalid_request"},
		{"zero grams", func(m map[string]any) {
			m["food_items"].([]map[string]any)[0]["ingredients"].([]map[string]any)[0]["grams"] = 0
		}, 400, "invalid_request"},
		{"unresolved", func(m map[string]any) {
			m["food_items"].([]map[string]any)[0]["ingredients"].([]map[string]any)[0]["fdc_id"] = 0
		}, 422, "unresolved_ingredient"},
		{"unknown food", func(m map[string]any) {
			m["food_items"].([]map[string]any)[0]["ingredients"].([]map[string]any)[0]["fdc_id"] = 999999
		}, 422, "unresolved_ingredient"},
		{"unknown recognition", func(m map[string]any) { m["recognition_id"] = uuid.NewString() }, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ok()
			tt.edit(m)
			r := a.do("POST", "/v1/meal-entries", access, m, "Idempotency-Key", uuid.NewString())
			if r.status != tt.status || errorCode(t, r) != tt.code {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	if r := a.do("POST", "/v1/meal-entries", access, ok()); r.status != 400 {
		t.Fatalf("no key: %d", r.status)
	}
	for _, path := range []string{"/v1/days/2026-13-01", "/v1/days?from=2026-01-01&to=2026-06-01", fmt.Sprintf("/v1/meal-entries?cursor=%s", "x")} {
		if r := a.do("GET", path, access, nil); r.status != 400 {
			t.Fatalf("%s: %d", path, r.status)
		}
	}
	if code := errorCode(t, a.do("GET", "/v1/days/2026-11-22/summary", access, nil)); code != "not_found" {
		t.Fatalf("summary of empty day: %s", code)
	}
}
