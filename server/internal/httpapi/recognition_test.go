package httpapi

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/jobs"
)

type fakeGemini struct {
	mu    sync.Mutex
	texts []string
}

func (f *fakeGemini) Generate(context.Context, gemini.Request) (gemini.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return gemini.Response{}, gemini.ErrRetryable
	}
	t := f.texts[0]
	f.texts = f.texts[1:]
	return gemini.Response{Text: t, FinishReason: "STOP"}, nil
}

type memPhotos struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memPhotos) Put(_ context.Context, k string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	return "https://s3.example/" + k + "?X-Amz-Expires=900", nil
}

func (m *memPhotos) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

const dishOutput = `{"is_food":true,"food_items":[
 {"name_zh":"番茄炒蛋","name_en":"Tomato and egg stir-fry","cooking_method":"stir_fried","confidence":0.86,"estimated_kcal":260,
  "ingredients":[{"name_zh":"番茄","name_en":"tomato, raw","grams":150,"is_cooking_oil":false},
                 {"name_zh":"炒蛋","name_en":"egg, scrambled","grams":100,"is_cooking_oil":false},
                 {"name_zh":"植物油","name_en":"vegetable oil","grams":10,"is_cooking_oil":true}]}]}`

func testJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil)
	return buf.Bytes()
}

func (a *testAPI) uploadPhoto(token, key string, jpg []byte, meta string) response {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if jpg != nil {
		w, _ := mw.CreateFormFile("photo", "meal.jpg")
		w.Write(jpg)
	}
	if meta != "" {
		mw.WriteField("meta", meta)
	}
	mw.Close()
	return a.do("POST", "/v1/recognitions", token, body.String(), "Content-Type", mw.FormDataContentType(), "Idempotency-Key", key)
}

func metaNow(a *testAPI) string {
	return `{"taken_at":"` + a.now.Add(-time.Minute).Format(time.RFC3339) + `","utc_offset_minutes":480}`
}

func TestRecognitionFlow(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	a.gem.texts = []string{dishOutput}
	key := uuid.NewString()
	jpg := testJPEG(t)
	r := a.uploadPhoto(access, key, jpg, metaNow(a))
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	assertShape(t, r.body, "recognition.json")
	m := r.json(t)
	id := m["id"].(string)
	if m["recognition_quota"].(map[string]any)["used"] != 1.0 || a.photos.count() != 1 {
		t.Fatalf("recognition = %s", r.body)
	}

	// Lost response: the same key returns the stored result.
	r = a.uploadPhoto(access, key, jpg, metaNow(a))
	if r.status != http.StatusOK || r.json(t)["id"] != id {
		t.Fatalf("replay: %d %s", r.status, r.body)
	}
	r = a.do("GET", "/v1/recognitions/"+id, access, nil)
	if r.status != 200 || r.json(t)["status"] != "succeeded" {
		t.Fatalf("get: %d %s", r.status, r.body)
	}

	tests := []struct {
		name   string
		key    string
		jpg    []byte
		meta   string
		status int
		code   string
	}{
		{"truncated jpeg", uuid.NewString(), jpg[:len(jpg)/2], metaNow(a), 400, "invalid_photo"},
		{"no key", "", jpg, metaNow(a), 400, "invalid_request"},
		{"no photo", uuid.NewString(), nil, metaNow(a), 400, "invalid_request"},
		{"no meta", uuid.NewString(), jpg, "", 400, "invalid_request"},
		{"bad offset", uuid.NewString(), jpg, `{"taken_at":"2026-11-22T04:12:00Z","utc_offset_minutes":900}`, 400, "invalid_request"},
		{"not jpeg", uuid.NewString(), []byte("png"), metaNow(a), 400, "invalid_photo"},
		{"model down", uuid.NewString(), jpg, metaNow(a), 503, "model_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := a.uploadPhoto(access, tt.key, tt.jpg, tt.meta)
			if r.status != tt.status {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			if code := errorCode(t, r); code != tt.code {
				t.Fatalf("code %s", code)
			}
		})
	}
	if r := a.uploadPhoto(access, key, jpgVariant(t), metaNow(a)); r.status != 409 || errorCode(t, r) != "idempotency_key_reused" {
		t.Fatalf("reused key: %d %s", r.status, r.body)
	}
	if code := errorCode(t, a.do("GET", "/v1/recognitions/"+uuid.NewString(), access, nil)); code != "not_found" {
		t.Fatalf("unknown id: %s", code)
	}
}

func jpgVariant(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil)
	return buf.Bytes()
}

func TestDeleteAccount(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	a.gem.texts = []string{dishOutput}
	if r := a.uploadPhoto(access, uuid.NewString(), testJPEG(t), metaNow(a)); r.status != 201 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	if r := a.do("DELETE", "/v1/me", access, map[string]string{"apple_authorization_code": "fresh"}); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if len(a.apple.revoked) != 1 || a.apple.revoked[0] != "rt-fresh" || a.photos.count() != 0 {
		t.Fatalf("revoked %v photos %d", a.apple.revoked, a.photos.count())
	}
	if code := errorCode(t, a.do("GET", "/v1/me", access, nil)); code != "unauthenticated" {
		t.Fatalf("after delete: %s", code)
	}
}

func TestDeleteAccountAsksForAppleReauth(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	a.apple.failRevoke = true // the stored refresh token no longer works
	r := a.do("DELETE", "/v1/me", access, nil)
	if r.status != http.StatusConflict || errorCode(t, r) != "apple_reauth_required" {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/v1/me", access, nil); r.status != 200 {
		t.Fatalf("user must still exist: %d", r.status)
	}
}

func TestCleanupJob(t *testing.T) {
	a := newTestAPI(t)
	access, _ := a.signInApple()
	a.gem.texts = []string{dishOutput}
	if r := a.uploadPhoto(access, uuid.NewString(), testJPEG(t), metaNow(a)); r.status != 201 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	runner := &jobs.Runner{Store: a.store, Photos: a.photos, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }}
	runner.Once(context.Background())
	if a.photos.count() != 0 {
		t.Fatalf("photos after cleanup = %d", a.photos.count())
	}
	if keys, err := a.store.PendingPhotoDeletions(context.Background(), 10); err != nil || len(keys) != 0 {
		t.Fatalf("queue = %v, %v", keys, err)
	}
}
