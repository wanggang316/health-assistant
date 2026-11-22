// Package alert sends operator alerts to a Feishu bot webhook.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Feishu posts text messages to a bot webhook. An empty URL only logs.
type Feishu struct {
	URL  string
	HTTP *http.Client
	Log  *slog.Logger
}

// Alert sends text. Errors are logged, not returned.
func (f *Feishu) Alert(ctx context.Context, text string) {
	if f.URL == "" {
		f.Log.Warn("alert (no webhook)", "text", text)
		return
	}
	body, _ := json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.URL, bytes.NewReader(body))
	if err != nil {
		f.Log.Error("alert", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	c := f.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		f.Log.Error("alert", "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		f.Log.Error("alert", "status", resp.StatusCode)
	}
}
