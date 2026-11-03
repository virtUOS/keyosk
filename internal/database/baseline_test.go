package database

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/database/migrations"
)

// appliedMigrations returns the names bun has recorded, in the order applied.
func appliedMigrations(t *testing.T, s *Store) []string {
	t.Helper()
	var names []string
	if err := s.db.NewRaw(`SELECT name FROM bun_migrations ORDER BY id`).
		Scan(context.Background(), &names); err != nil {
		t.Fatal(err)
	}
	return names
}

// recordedAs gives s the schema the baseline builds, which is 0.9.0's, and
// records it as built by the given migrations instead, as an earlier release
// would have.
func recordedAs(t *testing.T, s *Store, names []string) {
	t.Helper()
	ctx := context.Background()
	migrateThrough(t, s, migrations.BaselineName)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bun_migrations`); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO bun_migrations (name, group_id) VALUES (?, ?)`, name, i+1); err != nil {
			t.Fatal(err)
		}
	}
}

// A new database is built by the baseline alone.
func TestNewDatabaseRecordsOnlyTheBaseline(t *testing.T) {
	s := migratedStore(t, t.Name())
	if got, want := appliedMigrations(t, s), []string{migrations.BaselineName}; !slices.Equal(got, want) {
		t.Errorf("applied migrations = %v, want %v", got, want)
	}
}

// A database 0.9.0 has run on already has the baseline's schema. Starting on
// it records the baseline in place of the migrations it replaced, and keeps
// the data; running the baseline instead would fail on the existing tables.
func TestBaselineTakesOverFromTheRelease(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, t.Name())
	recordedAs(t, s, migrations.ReplacedByBaseline)
	if _, err := s.GetOrCreateUser(ctx, "sub-1", "a@example.org", "A"); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := s.RunMigrations(ctx); err != nil {
			t.Fatalf("RunMigrations on a 0.9.0 database: %v", err)
		}
		if got, want := appliedMigrations(t, s), []string{migrations.BaselineName}; !slices.Equal(got, want) {
			t.Errorf("applied migrations = %v, want %v", got, want)
		}
	}
	var users int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Errorf("users after taking over = %d, want the 1 there was", users)
	}
}

// A database an earlier release left has not run every migration up to 0.9.0,
// and nothing here can run the rest any more. Starting must fail with what to
// do, and leave the database as it was, so 0.9.0 can still upgrade it.
func TestDatabaseOlderThanTheReleaseIsRefused(t *testing.T) {
	s := testStore(t, t.Name())
	v081 := migrations.ReplacedByBaseline[:9]
	recordedAs(t, s, v081)

	err := s.RunMigrations(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upgrade to 0.9.0") {
		t.Errorf("RunMigrations on a v0.8.1 database = %v, want it to say to upgrade to 0.9.0 first", err)
	}
	if got := appliedMigrations(t, s); !slices.Equal(got, v081) {
		t.Errorf("applied migrations = %v, want %v left as they were", got, v081)
	}
}

// There is no schema to go back to before the baseline, and dropping every
// table would only lose the data. Rolling it back fails and changes nothing.
func TestBaselineCannotBeRolledBack(t *testing.T) {
	s := migratedStore(t, t.Name())
	before := dumpSchema(t, s.db)

	if _, err := newMigrator(s.db, migrations.Migrations).Rollback(context.Background()); err == nil {
		t.Error("rolling back the baseline succeeded")
	}
	if after := dumpSchema(t, s.db); after != before {
		t.Errorf("rolling back changed the schema:\n%s", lineDiff(before, after))
	}
}

// bun runs a new database's migrations in name order, so one that sorts
// before the baseline would run on a database that has no tables yet.
func TestEveryMigrationSortsAfterTheBaseline(t *testing.T) {
	if first := migrations.Migrations.Sorted()[0].Name; first != migrations.BaselineName {
		t.Errorf("first migration = %s, want the baseline, %s", first, migrations.BaselineName)
	}
}
