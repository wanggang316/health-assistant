// Package gemini is a thin client for generateContent on the Vertex AI
// express endpoint (ADR 0001, §6.2).
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the Vertex AI express endpoint.
const DefaultBaseURL = "https://aiplatform.googleapis.com/v1/publishers/google/models/"

// Client calls generateContent.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New returns a client for the express endpoint.
func New(apiKey string) *Client {
	return &Client{BaseURL: DefaultBaseURL, APIKey: apiKey, HTTP: &http.Client{}}
}

// Request is one structured-output call.
type Request struct {
	Model           string
	System          string
	Image           []byte // JPEG, optional
	Text            string
	Schema          map[string]any
	MaxOutputTokens int
	ThinkingLevel   string // MINIMAL, LOW, ...; empty for model default
	Timeout         time.Duration
}

// Usage is the token usage of a call.
type Usage struct {
	InputTokens   int
	OutputTokens  int
	ThoughtTokens int
}

// Response is the JSON text of the first candidate.
type Response struct {
	Text         string
	FinishReason string
	Usage        Usage
}

// Error kinds. A retryable error is a timeout, 429, or 5xx.
var (
	ErrRetryable = errors.New("gemini: retryable error")
	ErrBadFinish = errors.New("gemini: finish reason is not STOP")
)

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type responseFormat struct {
	Text struct {
		MimeType string         `json:"mimeType"`
		Schema   map[string]any `json:"schema"`
	} `json:"text"`
}

type generationConfig struct {
	ResponseFormat  []responseFormat `json:"responseFormat"`
	MaxOutputTokens int              `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *struct {
		ThinkingLevel string `json:"thinkingLevel"`
	} `json:"thinkingConfig,omitempty"`
}

type requestBody struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type responseBody struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

// Generate runs one call. Structured output uses generationConfig.responseFormat
// (checked on the express endpoint with both models on 2026-11-22).
func (c *Client) Generate(ctx context.Context, r Request) (Response, error) {
	var parts []part
	if r.Image != nil {
		parts = append(parts, part{InlineData: &inlineData{MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(r.Image)}})
	}
	if r.Text != "" {
		parts = append(parts, part{Text: r.Text})
	}
	body := requestBody{Contents: []content{{Role: "user", Parts: parts}}}
	if r.System != "" {
		body.SystemInstruction = &content{Parts: []part{{Text: r.System}}}
	}
	rf := responseFormat{}
	rf.Text.MimeType = "APPLICATION_JSON"
	rf.Text.Schema = r.Schema
	body.GenerationConfig = generationConfig{ResponseFormat: []responseFormat{rf}, MaxOutputTokens: r.MaxOutputTokens}
	if r.ThinkingLevel != "" {
		body.GenerationConfig.ThinkingConfig = &struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}{r.ThinkingLevel}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+r.Model+":generateContent", bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Timeouts and network errors are worth one retry.
		return Response{}, fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("%w: read body: %v", ErrRetryable, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return Response{}, fmt.Errorf("%w: status %d", ErrRetryable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("gemini: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var rb responseBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return Response{}, fmt.Errorf("gemini: decode response: %w", err)
	}
	out := Response{Usage: Usage{
		InputTokens: rb.UsageMetadata.PromptTokenCount, OutputTokens: rb.UsageMetadata.CandidatesTokenCount,
		ThoughtTokens: rb.UsageMetadata.ThoughtsTokenCount,
	}}
	if len(rb.Candidates) == 0 {
		return out, fmt.Errorf("%w: no candidates", ErrBadFinish)
	}
	cand := rb.Candidates[0]
	out.FinishReason = cand.FinishReason
	var sb strings.Builder
	for _, p := range cand.Content.Parts {
		if !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	out.Text = sb.String()
	if cand.FinishReason != "STOP" {
		return out, fmt.Errorf("%w: %s", ErrBadFinish, cand.FinishReason)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
