// Package nutrition reads the read-only nutrition.db (USDA FoodData Central
// subset plus curated aliases), matches ingredient names to foods, and
// calculates nutrients (ADR 0002, §7).
package nutrition

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)

// ErrNotFound means that no food has the ID.
var ErrNotFound = errors.New("food not found")

// Nutrients are absolute values, or values per 100 g in Food.Per100g.
type Nutrients struct {
	Kcal     float64  `json:"kcal"`
	ProteinG float64  `json:"protein_g"`
	FatG     float64  `json:"fat_g"`
	CarbG    float64  `json:"carb_g"`
	FiberG   *float64 `json:"fiber_g"`
	SodiumMg *float64 `json:"sodium_mg"`
}

// ForGrams returns per-100 g values scaled to grams.
func (n Nutrients) ForGrams(grams float64) Nutrients {
	k := grams / 100
	out := Nutrients{Kcal: n.Kcal * k, ProteinG: n.ProteinG * k, FatG: n.FatG * k, CarbG: n.CarbG * k}
	if n.FiberG != nil {
		v := *n.FiberG * k
		out.FiberG = &v
	}
	if n.SodiumMg != nil {
		v := *n.SodiumMg * k
		out.SodiumMg = &v
	}
	return out
}

func addOpt(a, b *float64) *float64 {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		v := *b
		return &v
	case b == nil:
		v := *a
		return &v
	}
	v := *a + *b
	return &v
}

// Add returns the sum. A missing fiber or sodium value counts as 0 when the
// other side has a value.
func (n Nutrients) Add(o Nutrients) Nutrients {
	return Nutrients{
		Kcal: n.Kcal + o.Kcal, ProteinG: n.ProteinG + o.ProteinG, FatG: n.FatG + o.FatG, CarbG: n.CarbG + o.CarbG,
		FiberG: addOpt(n.FiberG, o.FiberG), SodiumMg: addOpt(n.SodiumMg, o.SodiumMg),
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// Rounded rounds every value to 0.1 (output only, §7.3).
func (n Nutrients) Rounded() Nutrients {
	out := Nutrients{Kcal: round1(n.Kcal), ProteinG: round1(n.ProteinG), FatG: round1(n.FatG), CarbG: round1(n.CarbG)}
	if n.FiberG != nil {
		v := round1(*n.FiberG)
		out.FiberG = &v
	}
	if n.SodiumMg != nil {
		v := round1(*n.SodiumMg)
		out.SodiumMg = &v
	}
	return out
}

// Cross-check thresholds (ADR 0002, §7.4).
const (
	CrossCheckMinKcal = 30
	CrossCheckMaxDiff = 0.35
)

// NeedsGramCheck reports whether the database energy and the model energy of
// a food item differ by more than 35%.
func NeedsGramCheck(dbKcal, modelKcal float64) bool {
	if dbKcal < CrossCheckMinKcal {
		return false
	}
	return math.Abs(dbKcal-modelKcal)/dbKcal > CrossCheckMaxDiff
}

// Normalize prepares a name for alias lookup: NFKC, lower case, punctuation
// removed, spaces collapsed.
func Normalize(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// DB is an open nutrition.db.
type DB struct {
	db      *sql.DB
	Version string
}

// Open opens nutrition.db read-only.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	d := &DB{db: db}
	if err := db.QueryRow("SELECT value FROM meta WHERE key = 'nutrition_version'").Scan(&d.Version); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

const foodColumns = "f.fdc_id, f.data_type, f.description, COALESCE(f.category, ''), f.kcal, f.protein_g, f.fat_g, f.carb_g, f.fiber_g, f.sodium_mg"

func scanFood(row interface{ Scan(...any) error }, extra ...any) (Food, error) {
	var f Food
	var fiber, sodium sql.NullFloat64
	dest := append([]any{&f.FDCID, &f.DataType, &f.Description, &f.Category, &f.Per100g.Kcal, &f.Per100g.ProteinG, &f.Per100g.FatG, &f.Per100g.CarbG, &fiber, &sodium}, extra...)
	if err := row.Scan(dest...); err != nil {
		return f, err
	}
	if fiber.Valid {
		f.Per100g.FiberG = &fiber.Float64
	}
	if sodium.Valid {
		f.Per100g.SodiumMg = &sodium.Float64
	}
	return f, nil
}

// Food returns one food.
func (d *DB) Food(ctx context.Context, fdcID int64) (Food, error) {
	f, err := scanFood(d.db.QueryRowContext(ctx, "SELECT "+foodColumns+" FROM foods f WHERE f.fdc_id = ?", fdcID))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// Alias returns the food of an exact alias, if any.
func (d *DB) Alias(ctx context.Context, name, lang string) (Food, bool, error) {
	key := Normalize(name)
	if key == "" {
		return Food{}, false, nil
	}
	f, err := scanFood(d.db.QueryRowContext(ctx,
		"SELECT "+foodColumns+" FROM aliases a JOIN foods f ON f.fdc_id = a.fdc_id WHERE a.alias = ? AND a.lang = ?", key, lang))
	if errors.Is(err, sql.ErrNoRows) {
		return Food{}, false, nil
	}
	return f, err == nil, err
}

var stateWords = []string{"raw", "cooked", "boiled", "steamed", "fried", "roasted", "baked", "braised", "stewed", "grilled", "scrambled", "dried"}

func queryWords(name string) []string {
	var words []string
	seen := map[string]bool{}
	for _, w := range strings.Fields(Normalize(name)) {
		if len(w) < 2 || seen[w] || !isASCIIWord(w) {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return words
}

func isASCIIWord(w string) bool {
	for _, r := range w {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

var typeBonus = map[string]float64{"foundation": 0.6, "sr_legacy": 0.4, "fndds": 0}

// Candidates returns up to limit foods for an English ingredient name, from
// FTS5 BM25 with a bonus for Foundation > SR Legacy > FNDDS, for state words
// that match, and for word coverage (§7.2 step 3).
func (d *DB) Candidates(ctx context.Context, nameEn string, limit int) ([]Food, error) {
	words := queryWords(nameEn)
	if len(words) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + w + `"*`
	}
	rows, err := d.db.QueryContext(ctx,
		"SELECT "+foodColumns+", bm25(foods_fts) FROM foods_fts JOIN foods f ON f.fdc_id = foods_fts.rowid WHERE foods_fts MATCH ? ORDER BY bm25(foods_fts) LIMIT 60",
		strings.Join(quoted, " OR "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		f     Food
		score float64
	}
	var all []scored
	for rows.Next() {
		var bm float64
		f, err := scanFood(rows, &bm)
		if err != nil {
			return nil, err
		}
		desc := " " + Normalize(f.Description) + " "
		hit := 0
		for _, w := range words {
			if strings.Contains(desc, " "+w) {
				hit++
			}
		}
		score := -bm + typeBonus[f.DataType] + 3*float64(hit)/float64(len(words))
		for _, sw := range stateWords {
			if strings.Contains(" "+Normalize(nameEn)+" ", " "+sw+" ") && strings.Contains(desc, " "+sw+" ") {
				score += 0.8
			}
		}
		// Short descriptions are usually the plain food, not a mixed dish.
		score -= float64(strings.Count(f.Description, ",")) * 0.15
		all = append(all, scored{f, score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	out := make([]Food, 0, limit)
	for i := 0; i < len(all) && i < limit; i++ {
		out = append(out, all[i].f)
	}
	return out, nil
}

// Search finds foods for the manual input and ingredient change screens:
// aliases first (exact, then prefix, in both languages), then FTS.
func (d *DB) Search(ctx context.Context, q string, limit int) ([]Food, error) {
	key := Normalize(q)
	if key == "" {
		return nil, nil
	}
	seen := map[int64]bool{}
	var out []Food
	rows, err := d.db.QueryContext(ctx,
		"SELECT DISTINCT "+foodColumns+" FROM aliases a JOIN foods f ON f.fdc_id = a.fdc_id WHERE a.alias = ? OR a.alias LIKE ? ESCAPE '\\' ORDER BY (a.alias = ?) DESC, length(a.alias) LIMIT ?",
		key, escapeLike(key)+"%", key, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f, err := scanFood(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[f.FDCID] {
			seen[f.FDCID] = true
			out = append(out, f)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) < limit {
		more, err := d.Candidates(ctx, q, limit)
		if err != nil {
			return nil, err
		}
		for _, f := range more {
			if len(out) < limit && !seen[f.FDCID] {
				seen[f.FDCID] = true
				out = append(out, f)
			}
		}
	}
	return out, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// IsProduce reports whether the food is in a vegetable or fruit category
// (used by meal feedback facts, §8.2).
func (f Food) IsProduce() bool {
	c := strings.ToLower(f.Category)
	return strings.Contains(c, "vegetable") || strings.Contains(c, "fruit")
}
