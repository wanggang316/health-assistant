package nutrition

import (
	"context"
	"log/slog"
	"strings"
)

// Match methods, as stored in ingredients.match_method.
const (
	MethodAlias = "alias"
	MethodCache = "cache"
	MethodFTS   = "fts"
	MethodModel = "model"
	MethodUser  = "user"
)

// CandidateLimit is the number of candidates per ingredient (§7.2).
const CandidateLimit = 8

// Query is one ingredient name to match.
type Query struct {
	NameZh string
	NameEn string
}

// Match is the result for one query. Food is nil when unresolved; then
// Candidates has up to 5 foods for the user to choose from.
type Match struct {
	Food       *Food
	Method     string
	Candidates []Food
}

// PickItem is one unresolved ingredient for the model pick.
type PickItem struct {
	NameZh     string
	NameEn     string
	Candidates []Food
}

// Picker chooses a candidate per item, or 0 for none. It is the
// gemini-3.1-flash-lite call (§7.2 step 4).
type Picker interface {
	Pick(ctx context.Context, items []PickItem) ([]int64, error)
}

// MatchCache stores model picks by normalized English name (table
// ingredient_matches in health.db).
type MatchCache interface {
	CachedMatch(ctx context.Context, nameKey, version string) (int64, bool, error)
	SaveMatch(ctx context.Context, nameKey string, fdcID int64, version string) error
}

// Matcher resolves ingredient names: alias → cache → FTS candidates → model
// pick → unresolved.
type Matcher struct {
	DB     *DB
	Cache  MatchCache
	Picker Picker // nil disables the model pick
	Log    *slog.Logger
}

// Resolve matches every query. If the model pick fails, the best FTS
// candidate is used (method "fts"); a pick of "none" leaves the ingredient
// unresolved. Without a Picker, FTS-only ingredients stay unresolved.
func (m *Matcher) Resolve(ctx context.Context, qs []Query) ([]Match, error) {
	out := make([]Match, len(qs))
	var pending []int
	var items []PickItem
	for i, q := range qs {
		if f, ok, err := m.alias(ctx, q); err != nil {
			return nil, err
		} else if ok {
			out[i] = Match{Food: &f, Method: MethodAlias}
			continue
		}
		key := Normalize(q.NameEn)
		if key != "" && m.Cache != nil {
			id, ok, err := m.Cache.CachedMatch(ctx, key, m.DB.Version)
			if err != nil {
				return nil, err
			}
			if ok {
				f, err := m.DB.Food(ctx, id)
				if err == nil {
					out[i] = Match{Food: &f, Method: MethodCache}
					continue
				}
				if err != ErrNotFound {
					return nil, err
				}
			}
		}
		cands, err := m.DB.Candidates(ctx, q.NameEn, CandidateLimit)
		if err != nil {
			return nil, err
		}
		out[i] = Match{Candidates: cands}
		if len(cands) > 0 {
			pending = append(pending, i)
			items = append(items, PickItem{NameZh: q.NameZh, NameEn: q.NameEn, Candidates: cands})
		}
	}
	if len(items) > 0 && m.Picker != nil {
		picks, err := m.Picker.Pick(ctx, items)
		if err != nil {
			m.Log.Warn("ingredient pick failed", "err", err, "items", len(items))
		}
		for j, idx := range pending {
			if err != nil || j >= len(picks) {
				// The pick failed: use the best FTS candidate so that the
				// user still sees a value; the cross-check and the review
				// screen catch a bad match.
				f := items[j].Candidates[0]
				out[idx] = Match{Food: &f, Method: MethodFTS}
				continue
			}
			if picks[j] == 0 {
				continue
			}
			for _, c := range items[j].Candidates {
				if c.FDCID == picks[j] {
					f := c
					out[idx] = Match{Food: &f, Method: MethodModel}
					if key := Normalize(qs[idx].NameEn); key != "" && m.Cache != nil {
						if err := m.Cache.SaveMatch(ctx, key, f.FDCID, m.DB.Version); err != nil {
							return nil, err
						}
					}
					break
				}
			}
		}
	}
	for i := range out {
		if out[i].Food == nil && len(out[i].Candidates) > 5 {
			out[i].Candidates = out[i].Candidates[:5]
		}
		if out[i].Food != nil {
			out[i].Candidates = nil
		}
	}
	return out, nil
}

// alias tries the English name first: the model writes the cooked state
// there ("egg, scrambled"), while the Chinese name is often only the food
// ("鸡蛋"). Chinese names with "/" are tried per part.
func (m *Matcher) alias(ctx context.Context, q Query) (Food, bool, error) {
	if f, ok, err := m.DB.Alias(ctx, q.NameEn, "en"); err != nil || ok {
		return f, ok, err
	}
	for _, part := range strings.Split(q.NameZh, "/") {
		if f, ok, err := m.DB.Alias(ctx, part, "zh"); err != nil || ok {
			return f, ok, err
		}
	}
	return Food{}, false, nil
}
