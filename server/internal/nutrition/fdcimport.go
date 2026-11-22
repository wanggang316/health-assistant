package nutrition

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Dataset is one line of datasets.lock.
type Dataset struct {
	DataType string // foundation | sr_legacy | fndds
	Tag      string
	SHA256   string
	URL      string
}

// ParseLock reads datasets.lock.
func ParseLock(r io.Reader) ([]Dataset, error) {
	var ds []Dataset
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 {
			return nil, fmt.Errorf("datasets.lock: bad line %q", line)
		}
		ds = append(ds, Dataset{DataType: f[0], Tag: f[1], SHA256: f[2], URL: f[3]})
	}
	return ds, sc.Err()
}

// Fetch returns the path of the dataset zip in cacheDir. It downloads the file
// if it is missing and always checks the SHA-256.
func Fetch(ctx context.Context, d Dataset, cacheDir string) (string, error) {
	path := filepath.Join(cacheDir, filepath.Base(d.URL))
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := download(ctx, d.URL, path); err != nil {
			return "", err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != d.SHA256 {
		return "", fmt.Errorf("%s: sha256 %s, want %s", path, got, d.SHA256)
	}
	return path, nil
}

func download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FDC nutrient IDs (checked against the 2026-10 Foundation, SR Legacy, and
// FNDDS 2024-10 JSON files).
const (
	nutEnergyKcal     = 1008
	nutEnergyAtwaterS = 2048
	nutEnergyAtwaterG = 2047
	nutProtein        = 1003
	nutFat            = 1004
	nutCarb           = 1005
	nutFiber          = 1079
	nutSodium         = 1093
)

type fdcFood struct {
	FDCID        int64  `json:"fdcId"`
	Description  string `json:"description"`
	FoodCategory *struct {
		Description string `json:"description"`
	} `json:"foodCategory"`
	WWEIACategory *struct {
		Description string `json:"wweiaFoodCategoryDescription"`
	} `json:"wweiaFoodCategory"`
	FoodNutrients []struct {
		Nutrient struct {
			ID int `json:"id"`
		} `json:"nutrient"`
		Amount *float64 `json:"amount"`
	} `json:"foodNutrients"`
}

// Food is one row of foods: values per 100 g.
type Food struct {
	FDCID       int64     `json:"fdc_id"`
	DataType    string    `json:"data_type"`
	Description string    `json:"description"`
	Category    string    `json:"-"`
	Per100g     Nutrients `json:"per_100g"`
}

// toFood converts an FDC record. It returns false if protein, fat, or
// carbohydrate is missing.
func (f fdcFood) toFood(dataType string) (Food, bool) {
	vals := map[int]float64{}
	for _, n := range f.FoodNutrients {
		if n.Amount != nil {
			vals[n.Nutrient.ID] = *n.Amount
		}
	}
	p, okP := vals[nutProtein]
	fat, okF := vals[nutFat]
	c, okC := vals[nutCarb]
	if !okP || !okF || !okC {
		return Food{}, false
	}
	kcal, ok := vals[nutEnergyKcal]
	if !ok {
		kcal, ok = vals[nutEnergyAtwaterS]
	}
	if !ok {
		kcal, ok = vals[nutEnergyAtwaterG]
	}
	if !ok {
		kcal = 4*p + 9*fat + 4*c
	}
	food := Food{
		FDCID: f.FDCID, DataType: dataType, Description: strings.TrimSpace(f.Description),
		Per100g: Nutrients{Kcal: kcal, ProteinG: p, FatG: fat, CarbG: c},
	}
	if v, ok := vals[nutFiber]; ok {
		food.Per100g.FiberG = &v
	}
	if v, ok := vals[nutSodium]; ok {
		food.Per100g.SodiumMg = &v
	}
	switch {
	case f.FoodCategory != nil:
		food.Category = f.FoodCategory.Description
	case f.WWEIACategory != nil:
		food.Category = f.WWEIACategory.Description
	}
	return food, true
}

// readZipFoods streams the foods of the single JSON file in a dataset zip.
func readZipFoods(zipPath, dataType string, fn func(Food) error) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) != 1 {
		return fmt.Errorf("%s: want one file, got %d", zipPath, len(zr.File))
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return readFoods(rc, dataType, fn)
}

// readFoods reads {"<Name>Foods": [ ... ]} one food at a time.
func readFoods(r io.Reader, dataType string, fn func(Food) error) error {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	if _, err := dec.Token(); err != nil { // {
		return err
	}
	if _, err := dec.Token(); err != nil { // key
		return err
	}
	if _, err := dec.Token(); err != nil { // [
		return err
	}
	for dec.More() {
		var f fdcFood
		if err := dec.Decode(&f); err != nil {
			return err
		}
		if food, ok := f.toFood(dataType); ok {
			if err := fn(food); err != nil {
				return err
			}
		}
	}
	return nil
}

const nutritionSchema = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE foods (
  fdc_id      INTEGER PRIMARY KEY,
  data_type   TEXT NOT NULL CHECK (data_type IN ('foundation', 'sr_legacy', 'fndds')),
  description TEXT NOT NULL,
  category    TEXT,
  kcal        REAL NOT NULL,
  protein_g   REAL NOT NULL,
  fat_g       REAL NOT NULL,
  carb_g      REAL NOT NULL,
  fiber_g     REAL,
  sodium_mg   REAL
);
CREATE VIRTUAL TABLE foods_fts USING fts5(description, content='foods', content_rowid='fdc_id');
CREATE TABLE aliases (
  alias  TEXT NOT NULL,
  lang   TEXT NOT NULL CHECK (lang IN ('zh', 'en')),
  fdc_id INTEGER NOT NULL REFERENCES foods(fdc_id),
  PRIMARY KEY (alias, lang)
);
`

// Source is one input of Build.
type Source struct {
	Dataset Dataset
	ZipPath string
}

// Build writes a new nutrition.db at outPath from the dataset zips and the
// alias CSV (columns alias,lang,fdc_id). Every alias must point to an
// imported food.
func Build(ctx context.Context, outPath string, sources []Source, aliasCSV io.Reader) (version string, err error) {
	if err := os.Remove(outPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	db, err := sql.Open("sqlite", "file:"+outPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)")
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, nutritionSchema); err != nil {
		return "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO foods (fdc_id, data_type, description, category, kcal, protein_g, fat_g, carb_g, fiber_g, sodium_mg) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return "", err
	}
	var tags []string
	for _, s := range sources {
		tags = append(tags, s.Dataset.Tag)
		err := readZipFoods(s.ZipPath, s.Dataset.DataType, func(f Food) error {
			_, err := ins.ExecContext(ctx, f.FDCID, f.DataType, f.Description, f.Category,
				f.Per100g.Kcal, f.Per100g.ProteinG, f.Per100g.FatG, f.Per100g.CarbG, f.Per100g.FiberG, f.Per100g.SodiumMg)
			return err
		})
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Dataset.DataType, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO foods_fts (rowid, description) SELECT fdc_id, description FROM foods"); err != nil {
		return "", err
	}
	if err := importAliases(ctx, tx, aliasCSV); err != nil {
		return "", err
	}
	version = strings.Join(tags, "+")
	if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('nutrition_version', ?)", version); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx, "VACUUM")
	return version, err
}

func importAliases(ctx context.Context, tx *sql.Tx, r io.Reader) error {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = 3
	rows, err := cr.ReadAll()
	if err != nil {
		return fmt.Errorf("aliases.csv: %w", err)
	}
	var missing []string
	for i, row := range rows {
		if i == 0 && row[0] == "alias" {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(row[2]), 10, 64)
		if err != nil {
			return fmt.Errorf("aliases.csv line %d: bad fdc_id %q", i+1, row[2])
		}
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM foods WHERE fdc_id = ?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			missing = append(missing, fmt.Sprintf("%s->%d", row[0], id))
			continue
		}
		lang := strings.TrimSpace(row[1])
		if _, err := tx.ExecContext(ctx, "INSERT INTO aliases (alias, lang, fdc_id) VALUES (?, ?, ?)", Normalize(row[0]), lang, id); err != nil {
			return fmt.Errorf("aliases.csv line %d (%s): %w", i+1, row[0], err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("aliases.csv: unknown fdc_id: %s", strings.Join(missing, ", "))
	}
	return nil
}
