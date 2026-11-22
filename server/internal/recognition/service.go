package recognition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/localtime"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
	"github.com/wanggang316/health-assistant/server/internal/photo"
	"github.com/wanggang316/health-assistant/server/internal/quota"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

// Limits (§6.5).
const (
	AttemptTimeout  = 30 * time.Second
	PipelineTimeout = 60 * time.Second
	MaxOutputTokens = 2048
	retryBackoff    = time.Second
)

// Errors of Recognize. Store errors (quota, idempotency) pass through.
var (
	ErrModelOutputInvalid = errors.New("model output invalid")
	ErrModelUnavailable   = errors.New("model unavailable")
)

// Generator is the gemini client.
type Generator interface {
	Generate(ctx context.Context, r gemini.Request) (gemini.Response, error)
}

// Alerter sends an operator alert (the Feishu webhook).
type Alerter interface {
	Alert(ctx context.Context, text string)
}

// Service runs the recognition pipeline (§6.1).
type Service struct {
	Store       *store.Store
	Gemini      Generator
	Model       string
	Photos      photo.Storage
	Matcher     *nutrition.Matcher
	Alerter     Alerter
	QuotaLimit  int
	GlobalLimit int
	Log         *slog.Logger
	Now         func() time.Time

	alertMu   sync.Mutex
	alertDate string
}

// Meta is the meta part of the upload.
type Meta struct {
	TakenAt          time.Time `json:"taken_at"`
	UTCOffsetMinutes int       `json:"utc_offset_minutes"`
}

// Outcome is the result of Recognize.
type Outcome struct {
	Recognition store.Recognition
	Result      Result
	Replay      bool
	QuotaUsed   int
	QuotaLimit  int
	ResetsAt    time.Time
}

// RequestHash identifies the request body for idempotency.
func RequestHash(photoBytes []byte, m Meta) string {
	h := sha256.New()
	h.Write(photoBytes)
	fmt.Fprintf(h, "|%s|%d", m.TakenAt.UTC().Format(time.RFC3339), m.UTCOffsetMinutes)
	return hex.EncodeToString(h.Sum(nil))
}

// Recognize runs the pipeline for one upload. The photo must already pass
// photo.Process. The pipeline runs in a context that the client cannot
// cancel, so a retry with the same key finds the result.
func (s *Service) Recognize(ctx context.Context, userID, key string, p photo.Processed, rawHash string, m Meta) (Outcome, error) {
	now := s.Now()
	quotaDate, resets := quota.Day(now, m.UTCOffsetMinutes)
	res, err := s.Store.ReserveRecognition(ctx, store.NewRecognition{
		UserID: userID, IdempotencyKey: key, RequestHash: rawHash, Model: s.Model,
		TakenAt: m.TakenAt, UTCOffset: m.UTCOffsetMinutes, QuotaDate: quotaDate,
		UTCDate: now.UTC().Format(time.DateOnly), QuotaLimit: s.QuotaLimit, GlobalLimit: s.GlobalLimit,
	}, now)
	out := Outcome{QuotaLimit: s.QuotaLimit, ResetsAt: resets}
	if errors.Is(err, store.ErrGlobalCap) {
		s.alertGlobalCap(now)
		return out, ErrModelUnavailable
	}
	if err != nil {
		return out, err
	}
	out.QuotaUsed = res.QuotaUsed
	if res.Replay {
		out.Recognition, out.Replay = res.Recognition, true
		if err := json.Unmarshal([]byte(res.Recognition.ResultJSON), &out.Result); err != nil {
			return out, err
		}
		_, out.ResetsAt = quota.Day(now, res.Recognition.UTCOffset)
		return out, nil
	}

	type done struct {
		rec    store.Recognition
		result Result
		err    error
	}
	ch := make(chan done, 1)
	go func() {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PipelineTimeout)
		defer cancel()
		rec, result, err := s.run(pctx, res.Recognition, p, m)
		ch <- done{rec, result, err}
	}()
	select {
	case d := <-ch:
		if d.err != nil {
			return out, d.err
		}
		out.Recognition, out.Result = d.rec, d.result
		if !d.result.IsFood {
			out.Result.FoodItems = []FoodItemResult{}
		}
		return out, nil
	case <-ctx.Done():
		return out, ctx.Err()
	}
}

func (s *Service) run(ctx context.Context, rec store.Recognition, p photo.Processed, m Meta) (store.Recognition, Result, error) {
	start := time.Now()
	photoID := uuid.Must(uuid.NewV7()).String()
	key := photo.Key(rec.UserID, photoID)

	putErr := make(chan error, 1)
	go func() { putErr <- s.Photos.Put(ctx, key, p.JPEG) }()

	output, raw, usage, attempts, genErr := s.generate(ctx, p.JPEG, m)
	uploadErr := <-putErr

	fail := func(code string, cause error) (store.Recognition, Result, error) {
		if uploadErr == nil {
			if err := s.Photos.Delete(context.WithoutCancel(ctx), key); err != nil {
				_ = s.Store.QueuePhotoDeletion(context.WithoutCancel(ctx), key)
			}
		}
		if err := s.Store.FailRecognition(context.WithoutCancel(ctx), rec.UserID, rec.ID, code, attempts, s.Now()); err != nil {
			s.Log.Error("fail recognition", "recognition_id", rec.ID, "err", err)
		}
		s.Log.Warn("recognition failed", "recognition_id", rec.ID, "code", code, "attempts", attempts, "err", cause)
		if code == "model_output_invalid" {
			return rec, Result{}, ErrModelOutputInvalid
		}
		if code == "model_unavailable" {
			return rec, Result{}, ErrModelUnavailable
		}
		return rec, Result{}, cause
	}
	if genErr != nil {
		if errors.Is(genErr, ErrModelOutputInvalid) {
			return fail("model_output_invalid", genErr)
		}
		return fail("model_unavailable", genErr)
	}
	if uploadErr != nil {
		return fail("internal", fmt.Errorf("photo upload: %w", uploadErr))
	}

	mealType := localtime.SuggestMealType(m.TakenAt, m.UTCOffsetMinutes)
	result, err := BuildResult(ctx, s.Matcher, output, mealType)
	if err != nil {
		return fail("internal", err)
	}
	resultJSON, _ := json.Marshal(result)
	usageDate := s.Now().UTC().Format(time.DateOnly)
	if err := s.Store.AddModelUsage(ctx, usageDate, s.Model, "recognition", 0, usage.InputTokens, usage.OutputTokens+usage.ThoughtTokens); err != nil {
		s.Log.Error("model usage", "err", err)
	}
	latency := time.Since(start).Milliseconds()
	err = s.Store.CompleteRecognition(ctx, rec.UserID, rec.ID, photoID, store.CompletedRecognition{
		PhotoKey: key, PhotoWidth: p.Width, PhotoHeight: p.Height, PhotoBytes: len(p.JPEG),
		IsFood: output.IsFood, PromptVersion: PromptVersion, NutritionVersion: s.Matcher.DB.Version,
		ModelOutputJSON: raw, ResultJSON: string(resultJSON), Attempts: attempts,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ThoughtTokens: usage.ThoughtTokens, LatencyMs: latency,
	}, s.Now())
	if err != nil {
		return fail("internal", err)
	}
	s.Log.Info("recognition", "recognition_id", rec.ID, "model", s.Model, "attempts", attempts, "food_items", len(output.FoodItems),
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "thought_tokens", usage.ThoughtTokens, "latency_ms", latency)
	rec.Status, rec.IsFood, rec.PhotoID, rec.PhotoKey, rec.ResultJSON, rec.ModelOutputJSON = "succeeded", output.IsFood, photoID, key, string(resultJSON), raw
	return rec, result, nil
}

// generate calls the model with at most one retry (§6.5). The retry after a
// validation failure tells the model what was wrong.
func (s *Service) generate(ctx context.Context, jpeg []byte, m Meta) (ModelOutput, string, gemini.Usage, int, error) {
	local := localtime.Local(m.TakenAt, m.UTCOffsetMinutes)
	text := fmt.Sprintf("Local time: %s, %s. Meal type guess: %s.", local.Format("15:04"), local.Weekday(),
		localtime.SuggestMealType(m.TakenAt, m.UTCOffsetMinutes))
	var usage gemini.Usage
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		req := gemini.Request{
			Model: s.Model, System: SystemPrompt, Image: jpeg, Text: text, Schema: Schema(),
			MaxOutputTokens: MaxOutputTokens, ThinkingLevel: "LOW", Timeout: AttemptTimeout,
		}
		var ve *ValidationError
		if errors.As(lastErr, &ve) {
			req.Text = text + "\nYour previous answer had these problems; fix them: " + strings.Join(ve.Problems, "; ")
		}
		resp, err := s.Gemini.Generate(ctx, req)
		usage.InputTokens += resp.Usage.InputTokens
		usage.OutputTokens += resp.Usage.OutputTokens
		usage.ThoughtTokens += resp.Usage.ThoughtTokens
		if err == nil {
			out, perr := Parse(resp.Text)
			if perr == nil {
				return out, resp.Text, usage, attempt, nil
			}
			if errors.As(perr, &ve) && ve.OilOnly && attempt == 2 {
				s.Log.Warn("accepting output with cooking oil problems", "problems", ve.Problems)
				return out, resp.Text, usage, attempt, nil
			}
			lastErr = perr
		} else {
			lastErr = err
			if !errors.Is(err, gemini.ErrRetryable) && !errors.Is(err, gemini.ErrBadFinish) {
				return ModelOutput{}, "", usage, attempt, err
			}
		}
		if attempt == 1 && ctx.Err() == nil {
			select {
			case <-time.After(retryBackoff):
			case <-ctx.Done():
			}
		}
	}
	var ve *ValidationError
	if errors.As(lastErr, &ve) || errors.Is(lastErr, gemini.ErrBadFinish) {
		return ModelOutput{}, "", usage, 2, fmt.Errorf("%w: %v", ErrModelOutputInvalid, lastErr)
	}
	return ModelOutput{}, "", usage, 2, lastErr
}

func (s *Service) alertGlobalCap(now time.Time) {
	date := now.UTC().Format(time.DateOnly)
	s.alertMu.Lock()
	first := s.alertDate != date
	s.alertDate = date
	s.alertMu.Unlock()
	s.Log.Error("global recognition cap reached", "limit", s.GlobalLimit, "date", date)
	if first && s.Alerter != nil {
		go s.Alerter.Alert(context.Background(), fmt.Sprintf("health-assistant: global recognition cap %d reached for %s (UTC). Recognitions return 503 until the next UTC day.", s.GlobalLimit, date))
	}
}
