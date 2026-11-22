-- Initial schema. See docs/design/technical-design.md §4.

CREATE TABLE users (
  id               TEXT PRIMARY KEY,
  provider         TEXT NOT NULL CHECK (provider IN ('apple', 'google')),
  provider_subject TEXT NOT NULL,
  email            TEXT,
  email_is_private INTEGER NOT NULL DEFAULT 0,
  display_name     TEXT,
  locale           TEXT NOT NULL DEFAULT 'en',      -- 'en' | 'zh-Hans', from Accept-Language
  state            TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'deleting')),
  created_at       TEXT NOT NULL,
  UNIQUE (provider, provider_subject)                -- ADR 0004: one user per sign-in identity
);

CREATE TABLE apple_credentials (
  user_id           TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  refresh_token_enc BLOB NOT NULL,                   -- AES-256-GCM, nonce || ciphertext
  updated_at        TEXT NOT NULL
);

CREATE TABLE auth_nonces (
  nonce_hash TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id          TEXT NOT NULL,
  access_token_hash  TEXT NOT NULL UNIQUE,
  access_expires_at  TEXT NOT NULL,
  refresh_token_hash TEXT NOT NULL UNIQUE,
  refresh_expires_at TEXT NOT NULL,
  rotated_at         TEXT,
  revoked_at         TEXT,
  created_at         TEXT NOT NULL
);
CREATE INDEX sessions_family ON sessions(family_id);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE profiles (
  user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  sex            TEXT NOT NULL CHECK (sex IN ('male', 'female')),
  birth_year     INTEGER NOT NULL,                   -- age = current year - birth_year
  height_cm      REAL NOT NULL CHECK (height_cm BETWEEN 100 AND 250),
  weight_kg      REAL NOT NULL CHECK (weight_kg BETWEEN 30 AND 300),
  activity_level TEXT NOT NULL CHECK (activity_level IN ('sedentary', 'light', 'moderate', 'active', 'very_active')),
  goal           TEXT NOT NULL CHECK (goal IN ('lose', 'keep', 'gain')),
  protein_pct    INTEGER NOT NULL DEFAULT 25,
  fat_pct        INTEGER NOT NULL DEFAULT 30,
  carb_pct       INTEGER NOT NULL DEFAULT 45,
  updated_at     TEXT NOT NULL,
  CHECK (protein_pct + fat_pct + carb_pct = 100)
);

CREATE TABLE meal_photos (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  s3_key     TEXT NOT NULL UNIQUE,                   -- meal-photos/<user_id>/<photo_id>.jpg
  width      INTEGER NOT NULL,
  height     INTEGER NOT NULL,
  bytes      INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

-- A recognition is immutable after it completes. It is not a record (glossary).
CREATE TABLE recognitions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  idempotency_key    TEXT NOT NULL,
  request_hash       TEXT NOT NULL,                  -- sha256 of photo bytes + meta
  photo_id           TEXT REFERENCES meal_photos(id) ON DELETE SET NULL,
  status             TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
  is_food            INTEGER,
  model              TEXT NOT NULL,                  -- e.g. gemini-3.8-flash
  prompt_version     TEXT NOT NULL,
  nutrition_version  TEXT,
  model_output_json  TEXT,                           -- raw validated model JSON (original recognition)
  result_json        TEXT,                           -- normalized food items + matches + nutrients
  error_code         TEXT,
  attempts           INTEGER NOT NULL DEFAULT 0,
  input_tokens       INTEGER,
  output_tokens      INTEGER,
  thought_tokens     INTEGER,
  latency_ms         INTEGER,
  taken_at           TEXT NOT NULL,                  -- UTC, from the app
  utc_offset_minutes INTEGER NOT NULL,
  quota_date         TEXT NOT NULL,                  -- local date used for the recognition quota
  created_at         TEXT NOT NULL,
  completed_at       TEXT,
  UNIQUE (user_id, idempotency_key)
);
CREATE INDEX recognitions_unconfirmed ON recognitions(status, created_at);

CREATE TABLE meal_entries (
  id                      TEXT PRIMARY KEY,
  user_id                 TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source                  TEXT NOT NULL CHECK (source IN ('recognition', 'manual')),
  recognition_id          TEXT UNIQUE REFERENCES recognitions(id),   -- one entry per recognition
  photo_id                TEXT REFERENCES meal_photos(id) ON DELETE SET NULL,
  meal_type               TEXT NOT NULL CHECK (meal_type IN ('breakfast', 'lunch', 'dinner', 'snack')),
  eaten_at                TEXT NOT NULL,             -- UTC (ADR 0011)
  utc_offset_minutes      INTEGER NOT NULL CHECK (utc_offset_minutes BETWEEN -720 AND 840),
  entry_date              TEXT NOT NULL,             -- local date of eaten_at + offset, never recalculated
  confirmed_snapshot_json TEXT,                      -- food items as first confirmed (eval pair)
  edited_after_confirm    INTEGER NOT NULL DEFAULT 0,
  created_at              TEXT NOT NULL,
  updated_at              TEXT NOT NULL,
  CHECK ((source = 'recognition') = (recognition_id IS NOT NULL))
);
CREATE INDEX meal_entries_day ON meal_entries(user_id, entry_date);
CREATE INDEX meal_entries_history ON meal_entries(user_id, eaten_at DESC, id DESC);

CREATE TABLE food_items (
  id               TEXT PRIMARY KEY,
  meal_entry_id    TEXT NOT NULL REFERENCES meal_entries(id) ON DELETE CASCADE,
  position         INTEGER NOT NULL,
  name_zh          TEXT,
  name_en          TEXT,
  cooking_method   TEXT NOT NULL,
  model_confidence REAL,                              -- null for manual items
  model_kcal       REAL,                              -- model's own estimate, cross-check only (ADR 0002)
  needs_gram_check INTEGER NOT NULL DEFAULT 0,
  UNIQUE (meal_entry_id, position)
);

-- Nutrient snapshot: values are fixed at save time and do not change if nutrition.db changes.
CREATE TABLE ingredients (
  id                TEXT PRIMARY KEY,
  food_item_id      TEXT NOT NULL REFERENCES food_items(id) ON DELETE CASCADE,
  position          INTEGER NOT NULL,
  name_zh           TEXT,
  name_en           TEXT,
  is_cooking_oil    INTEGER NOT NULL DEFAULT 0,
  grams             REAL NOT NULL CHECK (grams > 0 AND grams <= 5000),
  fdc_id            INTEGER NOT NULL,
  fdc_description   TEXT NOT NULL,
  nutrition_version TEXT NOT NULL,
  match_method      TEXT NOT NULL CHECK (match_method IN ('alias', 'cache', 'fts', 'model', 'user')),
  kcal              REAL NOT NULL,
  protein_g         REAL NOT NULL,
  fat_g             REAL NOT NULL,
  carb_g            REAL NOT NULL,
  fiber_g           REAL,
  sodium_mg         REAL,
  UNIQUE (food_item_id, position)
);

-- Health advice cache (ADR 0005, 0012).
CREATE TABLE advice (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL CHECK (kind IN ('meal_feedback', 'daily_summary')),
  meal_entry_id TEXT REFERENCES meal_entries(id) ON DELETE CASCADE,
  entry_date    TEXT,
  locale        TEXT NOT NULL,
  facts_json    TEXT NOT NULL,
  facts_hash    TEXT NOT NULL,
  text          TEXT NOT NULL,
  model         TEXT NOT NULL,                       -- gemini-3.1-flash-lite or 'template'
  stale         INTEGER NOT NULL DEFAULT 0,
  generated_at  TEXT NOT NULL,
  CHECK ((kind = 'meal_feedback' AND meal_entry_id IS NOT NULL AND entry_date IS NULL)
      OR (kind = 'daily_summary' AND meal_entry_id IS NULL AND entry_date IS NOT NULL))
);
CREATE UNIQUE INDEX advice_meal ON advice(meal_entry_id, locale) WHERE kind = 'meal_feedback';
CREATE UNIQUE INDEX advice_day ON advice(user_id, entry_date, locale) WHERE kind = 'daily_summary';

CREATE TABLE recognition_quota (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  quota_date TEXT NOT NULL,
  used       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, quota_date)
);

CREATE TABLE model_usage_daily (                     -- global cost guard and reporting
  usage_date    TEXT NOT NULL,                       -- UTC date
  model         TEXT NOT NULL,
  purpose       TEXT NOT NULL CHECK (purpose IN ('recognition', 'match', 'advice')),
  requests      INTEGER NOT NULL DEFAULT 0,
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (usage_date, model, purpose)
);

CREATE TABLE ingredient_matches (                    -- learned name -> FDC food, global
  name_key          TEXT PRIMARY KEY,                -- normalized name_en
  fdc_id            INTEGER NOT NULL,
  method            TEXT NOT NULL CHECK (method IN ('model')),
  nutrition_version TEXT NOT NULL,
  created_at        TEXT NOT NULL
);

CREATE TABLE idempotency_keys (                      -- for POST /v1/meal-entries
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key           TEXT NOT NULL,
  request_hash  TEXT NOT NULL,
  status_code   INTEGER NOT NULL,
  response_json TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  PRIMARY KEY (user_id, key)
);

CREATE TABLE photo_deletions (                       -- retry queue, survives user deletion
  s3_key     TEXT PRIMARY KEY,
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  created_at TEXT NOT NULL
);
