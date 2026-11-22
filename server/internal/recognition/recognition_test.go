package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/nutrition/nutritiontest"
	"github.com/wanggang316/health-assistant/server/internal/photo"
	"github.com/wanggang316/health-assistant/server/internal/quota"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

const validOutput = `{"is_food":true,"food_items":[
 {"name_zh":"番茄炒蛋","name_en":"Tomato and egg stir-fry","cooking_method":"stir_fried","confidence":0.86,"estimated_kcal":260,
  "ingredients":[{"name_zh":"番茄","name_en":"tomato, raw","grams":150,"is_cooking_oil":false},
                 {"name_zh":"炒蛋","name_en":"egg, scrambled","grams":100,"is_cooking_oil":false},
                 {"name_zh":"植物油","name_en":"vegetable oil","grams":10,"is_cooking_oil":true}]},
 {"name_zh":"米饭","name_en":"White rice","cooking_method":"steamed","confidence":0.95,"estimated_kcal":500,
  "ingredients":[{"name_zh":"米饭","name_en":"white rice, cooked","grams":200,"is_cooking_oil":false}]}]}`

func TestParse(t *testing.T) {
	if _, err := Parse(validOutput); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		text    string
		oilOnly bool
	}{
		{"not json", "{", false},
		{"food without items", `{"is_food":true,"food_items":[]}`, false},
		{"not food with items", strings.Replace(validOutput, `"is_food":true`, `"is_food":false`, 1), false},
		{"empty name", strings.Replace(validOutput, `"name_zh":"米饭","name_en":"White rice"`, `"name_zh":" ","name_en":"White rice"`, 1), false},
		{"bad method", strings.Replace(validOutput, `"steamed"`, `"microwaved"`, 1), false},
		{"grams out of range", strings.Replace(validOutput, `"grams":200`, `"grams":2500`, 1), false},
		{"missing oil", strings.Replace(validOutput, `"is_cooking_oil":true`, `"is_cooking_oil":false`, 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.text)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v", err)
			}
			if ve.OilOnly != tt.oilOnly {
				t.Fatalf("OilOnly = %v: %v", ve.OilOnly, ve.Problems)
			}
		})
	}
	if out, err := Parse(`{"is_food":false,"food_items":[]}`); err != nil || out.IsFood {
		t.Fatalf("not food: %v", err)
	}
}

type fakeGemini struct {
	mu        sync.Mutex
	responses []func(gemini.Request) (gemini.Response, error)
	requests  []gemini.Request
}

func (f *fakeGemini) Generate(ctx context.Context, r gemini.Request) (gemini.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	if len(f.responses) == 0 {
		return gemini.Response{}, errors.New("no more responses")
	}
	next := f.responses[0]
	f.responses = f.responses[1:]
	return next(r)
}

func text(s string) func(gemini.Request) (gemini.Response, error) {
	return func(gemini.Request) (gemini.Response, error) {
		return gemini.Response{Text: s, FinishReason: "STOP", Usage: gemini.Usage{InputTokens: 1000, OutputTokens: 200}}, nil
	}
}

func failWith(err error) func(gemini.Request) (gemini.Response, error) {
	return func(gemini.Request) (gemini.Response, error) { return gemini.Response{}, err }
}

type memPhotos struct {
	mu      sync.Mutex
	objects map[string][]byte
	putErr  error
}

func (m *memPhotos) Put(_ context.Context, k string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	m.objects[k] = b
	return nil
}

func (m *memPhotos) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, k)
	return nil
}

func (m *memPhotos) PresignGet(_ context.Context, k string) (string, error) {
	return "https://s3.example/" + k, nil
}

type fixture struct {
	svc    *Service
	gem    *fakeGemini
	photos *memPhotos
	store  *store.Store
	user   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, _, err := st.UpsertUser(ctx, store.SignInIdentity{Provider: "apple", Subject: "s"}, "zh-Hans", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &fixture{gem: &fakeGemini{}, photos: &memPhotos{objects: map[string][]byte{}}, store: st, user: u.ID}
	f.svc = &Service{
		Store: st, Gemini: f.gem, Model: "gemini-test", Photos: f.photos,
		Matcher:    &nutrition.Matcher{DB: nutritiontest.Open(t), Cache: st, Log: log},
		QuotaLimit: 2, GlobalLimit: 100, Log: log, Now: time.Now,
	}
	return f
}

var testMeta = Meta{TakenAt: time.Date(2026, 11, 22, 4, 12, 0, 0, time.UTC), UTCOffsetMinutes: 480}

func (f *fixture) recognize(t *testing.T, key string) (Outcome, error) {
	t.Helper()
	p := photo.Processed{JPEG: []byte("jpeg-" + key), Width: 10, Height: 10}
	return f.svc.Recognize(context.Background(), f.user, key, p, RequestHash(p.JPEG, testMeta), testMeta)
}

func TestRecognizeSuccessAndReplay(t *testing.T) {
	f := newFixture(t)
	f.gem.responses = append(f.gem.responses, text(validOutput))
	out, err := f.recognize(t, "k1")
	if err != nil {
		t.Fatal(err)
	}
	r := out.Result
	if out.Replay || out.QuotaUsed != 1 || r.SuggestedMealType != "lunch" || len(r.FoodItems) != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	egg := r.FoodItems[0]
	// tomato 150 g (27) + scrambled egg unresolved (no picker, FTS only) + oil 10 g (88.4)
	if egg.Ingredients[0].Food == nil || *egg.Ingredients[0].MatchMethod != "alias" || egg.Ingredients[1].Food != nil ||
		len(egg.Ingredients[1].Candidates) == 0 || egg.NeedsGramCheck {
		t.Fatalf("egg dish = %+v", egg)
	}
	if egg.Nutrients.Kcal != 115.4 {
		t.Fatalf("egg dish kcal = %v", egg.Nutrients.Kcal)
	}
	rice := r.FoodItems[1]
	// rice 200 g = 260 kcal vs model 500: 92% off -> gram check
	if rice.Nutrients.Kcal != 260 || !rice.NeedsGramCheck {
		t.Fatalf("rice = %+v", rice)
	}
	if r.Nutrients.Kcal != 375.4 {
		t.Fatalf("meal kcal = %v", r.Nutrients.Kcal)
	}
	if len(f.photos.objects) != 1 || out.Recognition.PhotoKey == "" {
		t.Fatalf("photos = %v", f.photos.objects)
	}
	req := f.gem.requests[0]
	if !strings.Contains(req.Text, "12:12") || !strings.Contains(req.Text, "lunch") || req.Image == nil {
		t.Fatalf("request text %q", req.Text)
	}

	again, err := f.recognize(t, "k1")
	if err != nil || !again.Replay || again.Recognition.ID != out.Recognition.ID || again.QuotaUsed != 1 || len(f.gem.requests) != 1 {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	p := photo.Processed{JPEG: []byte("other")}
	if _, err := f.svc.Recognize(context.Background(), f.user, "k1", p, RequestHash(p.JPEG, testMeta), testMeta); !errors.Is(err, store.ErrIdempotencyKeyReused) {
		t.Fatalf("reused key: %v", err)
	}
}

func TestRecognizeRetriesInvalidOutputThenFails(t *testing.T) {
	f := newFixture(t)
	f.gem.responses = append(f.gem.responses, text(`{"is_food":true,"food_items":[]}`), text(validOutput))
	out, err := f.recognize(t, "k1")
	if err != nil || len(out.Result.FoodItems) != 2 {
		t.Fatalf("retry then valid: %v", err)
	}
	if !strings.Contains(f.gem.requests[1].Text, "food_items is empty") {
		t.Fatalf("retry text = %q", f.gem.requests[1].Text)
	}

	f.gem.responses = append(f.gem.responses, text("{"), text("{"))
	if _, err := f.recognize(t, "k2"); !errors.Is(err, ErrModelOutputInvalid) {
		t.Fatalf("invalid twice: %v", err)
	}
	// Quota refunded: only k1 counts. Photo of k2 deleted.
	day, _ := quota.Day(time.Now(), testMeta.UTCOffsetMinutes)
	if used, _ := f.store.RecognitionQuotaUsed(context.Background(), f.user, day); used != 1 {
		t.Fatalf("quota used = %d, want 1 after refund", used)
	}
	if len(f.photos.objects) != 1 {
		t.Fatalf("photos after failure = %d", len(f.photos.objects))
	}
}

func TestRecognizeModelUnavailableRefundsAndRetrySameKey(t *testing.T) {
	f := newFixture(t)
	f.gem.responses = append(f.gem.responses, failWith(gemini.ErrRetryable), failWith(gemini.ErrRetryable))
	if _, err := f.recognize(t, "k1"); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// The same key runs again after a failure.
	f.gem.responses = append(f.gem.responses, text(validOutput))
	out, err := f.recognize(t, "k1")
	if err != nil || out.Replay || out.QuotaUsed != 1 {
		t.Fatalf("retry same key: %+v, %v", out, err)
	}
}

func TestRecognizeQuotaAndGlobalCap(t *testing.T) {
	f := newFixture(t)
	for _, k := range []string{"a", "b"} {
		f.gem.responses = append(f.gem.responses, text(`{"is_food":false,"food_items":[]}`))
		out, err := f.recognize(t, k)
		if err != nil || out.Result.IsFood || out.Result.FoodItems == nil {
			t.Fatalf("not food: %+v %v", out, err)
		}
	}
	if _, err := f.recognize(t, "c"); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("quota: %v", err)
	}
	f.svc.QuotaLimit, f.svc.GlobalLimit = 10, 2
	alerts := &countAlerter{}
	f.svc.Alerter = alerts
	if _, err := f.recognize(t, "d"); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("global cap: %v", err)
	}
	f.recognize(t, "e")
	time.Sleep(50 * time.Millisecond)
	if alerts.n() != 1 {
		t.Fatalf("alerts = %d", alerts.n())
	}
}

type countAlerter struct {
	mu    sync.Mutex
	count int
}

func (c *countAlerter) Alert(context.Context, string) {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
}

func (c *countAlerter) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func TestRecognizeSurvivesClientCancel(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.gem.responses = append(f.gem.responses, func(gemini.Request) (gemini.Response, error) {
		<-release
		return text(validOutput)(gemini.Request{})
	})
	ctx, cancel := context.WithCancel(context.Background())
	p := photo.Processed{JPEG: []byte("jpeg")}
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.Recognize(ctx, f.user, "k", p, RequestHash(p.JPEG, testMeta), testMeta)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Recognize(context.Background(), f.user, "k", p, RequestHash(p.JPEG, testMeta), testMeta); !errors.Is(err, store.ErrRecognitionInProgress) {
		t.Fatalf("in progress: %v", err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := f.svc.Recognize(context.Background(), f.user, "k", p, RequestHash(p.JPEG, testMeta), testMeta)
		if err == nil {
			if !out.Replay {
				t.Fatal("want replay")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pipeline did not finish after client cancel")
}

func TestGeminiPicker(t *testing.T) {
	g := &fakeGemini{responses: []func(gemini.Request) (gemini.Response, error){text(`{"i0":"10","i1":"none"}`)}}
	p := &GeminiPicker{Gemini: g, Model: "lite"}
	ids, err := p.Pick(context.Background(), []nutrition.PickItem{
		{NameEn: "egg, scrambled", Candidates: []nutrition.Food{{FDCID: 10, Description: "Egg, scrambled"}, {FDCID: 2}}},
		{NameEn: "x", Candidates: []nutrition.Food{{FDCID: 3}}},
	})
	if err != nil || len(ids) != 2 || ids[0] != 10 || ids[1] != 0 {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	schema, _ := json.Marshal(g.requests[0].Schema)
	if !strings.Contains(string(schema), `"enum":["none","10","2"]`) {
		t.Fatalf("schema = %s", schema)
	}
}
