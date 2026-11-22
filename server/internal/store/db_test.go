package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrateFromZeroIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.CheckSchema(ctx); err == nil {
		t.Fatal("CheckSchema on empty database: want pending error")
	}
	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 || applied[0] != "0001_init" {
		t.Fatalf("applied = %v", applied)
	}
	again, err := s.Migrate(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second Migrate = %v, %v", again, err)
	}
	if unknown, err := s.CheckSchema(ctx); err != nil || len(unknown) != 0 {
		t.Fatalf("CheckSchema = %v, %v", unknown, err)
	}
}

func TestCheckSchemaAllowsNewerVersions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.w.Exec("INSERT INTO schema_migrations VALUES ('9999_future', '2026-11-22T00:00:00.000Z')"); err != nil {
		t.Fatal(err)
	}
	unknown, err := s.CheckSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 || unknown[0] != "9999_future" {
		t.Fatalf("unknown = %v", unknown)
	}
}

func TestForeignKeysAreOn(t *testing.T) {
	s := newTestStore(t)
	_, err := s.w.Exec("INSERT INTO sessions (id, user_id, family_id, access_token_hash, access_expires_at, refresh_token_hash, refresh_expires_at, created_at) VALUES ('s','missing','f','a','x','r','x','x')")
	if err == nil {
		t.Fatal("insert with unknown user_id: want foreign key error")
	}
}

// nutrition.db uses FTS5 (§7.2); the driver must include it.
func TestFTS5Available(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.w.Exec("CREATE VIRTUAL TABLE t USING fts5(x)"); err != nil {
		t.Fatalf("fts5: %v", err)
	}
}
