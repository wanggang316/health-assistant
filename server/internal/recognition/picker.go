package recognition

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/gemini"
	"github.com/wanggang316/health-assistant/server/internal/nutrition"
)

// PickTimeout is the limit of the match pick call (§6.5).
const PickTimeout = 8 * time.Second

// UsageRecorder records model usage.
type UsageRecorder interface {
	AddModelUsage(ctx context.Context, date, model, purpose string, requests, in, out int) error
}

// GeminiPicker asks gemini-3.1-flash-lite to choose one candidate per
// ingredient. The schema lets the model answer only a candidate ID or
// "none" (§7.2 step 4).
type GeminiPicker struct {
	Gemini Generator
	Model  string
	Usage  UsageRecorder
}

const pickPrompt = `You match ingredient names from a meal photo to foods in the USDA FoodData Central database.
For each ingredient, choose the candidate that best matches the food and its state as eaten (raw, cooked, fried, and so on).
Answer "none" if no candidate is the same food.`

// Pick returns one FDC ID per item, or 0 for none.
func (p *GeminiPicker) Pick(ctx context.Context, items []nutrition.PickItem) ([]int64, error) {
	props := map[string]any{}
	var required []string
	var b strings.Builder
	for i, it := range items {
		key := "i" + strconv.Itoa(i)
		enum := []string{"none"}
		fmt.Fprintf(&b, "%s: %s (%s)\n", key, it.NameEn, it.NameZh)
		for _, c := range it.Candidates {
			id := strconv.FormatInt(c.FDCID, 10)
			enum = append(enum, id)
			fmt.Fprintf(&b, "  %s = %s\n", id, c.Description)
		}
		props[key] = map[string]any{"type": "string", "enum": enum}
		required = append(required, key)
	}
	resp, err := p.Gemini.Generate(ctx, gemini.Request{
		Model: p.Model, System: pickPrompt, Text: b.String(),
		Schema:          map[string]any{"type": "object", "properties": props, "required": required, "propertyOrdering": required},
		MaxOutputTokens: 1024, ThinkingLevel: "MINIMAL", Timeout: PickTimeout,
	})
	if p.Usage != nil {
		_ = p.Usage.AddModelUsage(context.WithoutCancel(ctx), time.Now().UTC().Format(time.DateOnly), p.Model, "match", 1,
			resp.Usage.InputTokens, resp.Usage.OutputTokens+resp.Usage.ThoughtTokens)
	}
	if err != nil {
		return nil, err
	}
	var answer map[string]string
	if err := json.Unmarshal([]byte(resp.Text), &answer); err != nil {
		return nil, fmt.Errorf("pick output: %w", err)
	}
	out := make([]int64, len(items))
	for i := range items {
		v := answer["i"+strconv.Itoa(i)]
		if v == "" || v == "none" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		out[i] = id // Matcher ignores an ID that is not a candidate.
	}
	return out, nil
}
