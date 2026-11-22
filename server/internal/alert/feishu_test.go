package alert

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFeishuSignedMessage(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	f := &Feishu{URL: srv.URL, Secret: "s", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Unix(1700000000, 0) }}
	f.Alert(context.Background(), "cap reached")
	if got["msg_type"] != "text" || got["timestamp"] != "1700000000" || got["sign"] != sign(1700000000, "s") {
		t.Fatalf("message = %v", got)
	}
	if got["content"].(map[string]any)["text"] != "cap reached" {
		t.Fatalf("content = %v", got["content"])
	}
}
