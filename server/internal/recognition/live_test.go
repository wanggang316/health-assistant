//go:build live

package recognition

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/photo"
)

// TestLiveRecognition calls Vertex AI with a real key and photo:
//
//	GEMINI_API_KEY=... NUTRITION_DB=nutrition.db LIVE_PHOTO=meal.jpg go test -tags live -run Live -v ./internal/recognition
func TestLiveRecognition(t *testing.T) {
	f := newFixture(t)
	db, err := nutrition.Open(os.Getenv("NUTRITION_DB"))
	if err != nil {
		t.Fatal(err)
	}
	g := gemini.New(os.Getenv("GEMINI_API_KEY"))
	f.svc.Gemini, f.svc.Model = g, "gemini-3.8-flash"
	f.svc.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	f.svc.Matcher = &nutrition.Matcher{DB: db, Cache: f.store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Picker: &GeminiPicker{Gemini: g, Model: "gemini-3.1-flash-lite", Usage: f.store}}
	raw, err := os.ReadFile(os.Getenv("LIVE_PHOTO"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := photo.Process(raw)
	if err != nil {
		t.Fatal(err)
	}
	m := Meta{TakenAt: time.Now().Add(-time.Minute), UTCOffsetMinutes: 480}
	start := time.Now()
	out, err := f.svc.Recognize(context.Background(), f.user, "live", p, RequestHash(raw, m), m)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(out.Result, "", " ")
	t.Logf("latency %s\n%s", time.Since(start), b)
}
