// Package alert sends operator alerts to a Feishu bot webhook.
package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Feishu posts text messages to a bot webhook. An empty URL only logs.
type Feishu struct {
	URL    string
	Secret string // optional signing secret of the bot
	HTTP   *http.Client
	Log    *slog.Logger
	Now    func() time.Time
}

// sign returns the Feishu bot signature: base64(HMAC-SHA256 with key
// "<timestamp>\n<secret>" over an empty message).
func sign(ts int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", ts, secret)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Alert sends text. Errors are logged, not returned.
func (f *Feishu) Alert(ctx context.Context, text string) {
	if f.URL == "" {
		f.Log.Warn("alert (no webhook)", "text", text)
		return
	}
	msg := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if f.Secret != "" {
		now := time.Now
		if f.Now != nil {
			now = f.Now
		}
		ts := now().Unix()
		msg["timestamp"] = strconv.FormatInt(ts, 10)
		msg["sign"] = sign(ts, f.Secret)
	}
	body, _ := json.Marshal(msg)
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
