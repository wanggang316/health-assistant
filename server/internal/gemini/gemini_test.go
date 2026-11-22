package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	var got map[string]any
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-goog-api-key")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true},{"text":"{\"a\":"},{"text":"1}"}]},"finishReason":"STOP"}],
		  "usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":3}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/m/", APIKey: "k", HTTP: srv.Client()}
	resp, err := c.Generate(context.Background(), Request{
		Model: "gemini-x", System: "sys", Image: []byte{1, 2}, Text: "hi",
		Schema: map[string]any{"type": "object"}, MaxOutputTokens: 100, ThinkingLevel: "LOW",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":1}` || resp.Usage != (Usage{10, 5, 3}) {
		t.Fatalf("resp = %+v", resp)
	}
	if gotPath != "/m/gemini-x:generateContent" || gotKey != "k" {
		t.Fatalf("path %s key %s", gotPath, gotKey)
	}
	gc := got["generationConfig"].(map[string]any)
	rf := gc["responseFormat"].([]any)[0].(map[string]any)["text"].(map[string]any)
	if rf["mimeType"] != "APPLICATION_JSON" || gc["thinkingConfig"].(map[string]any)["thinkingLevel"] != "LOW" {
		t.Fatalf("generationConfig = %v", gc)
	}
	parts := got["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if parts[0].(map[string]any)["inlineData"].(map[string]any)["data"] != "AQI=" {
		t.Fatalf("parts = %v", parts)
	}
}

func TestGenerateErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		delay   time.Duration
		wantErr error
	}{
		{"429", 429, `{}`, 0, ErrRetryable},
		{"500", 500, `{}`, 0, ErrRetryable},
		{"timeout", 200, `{}`, 300 * time.Millisecond, ErrRetryable},
		{"max tokens", 200, `{"candidates":[{"content":{"parts":[{"text":"{"}]},"finishReason":"MAX_TOKENS"}]}`, 0, ErrBadFinish},
		{"no candidates", 200, `{"candidates":[]}`, 0, ErrBadFinish},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(tt.delay):
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c := &Client{BaseURL: srv.URL + "/", APIKey: "k", HTTP: srv.Client()}
			_, err := c.Generate(context.Background(), Request{Model: "m", Text: "x", Timeout: 100 * time.Millisecond})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
	c := &Client{BaseURL: "http://127.0.0.1:1/", APIKey: "k", HTTP: http.DefaultClient}
	if _, err := c.Generate(context.Background(), Request{Model: "m", Text: "x"}); !errors.Is(err, ErrRetryable) {
		t.Fatalf("connection refused: %v", err)
	}
}
