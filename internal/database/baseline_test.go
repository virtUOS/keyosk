package database

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// bun's table does not make the name unique, so a record can be doubled by
// hand. A database with every migration up to 0.9.0 is one 0.9.0 has run on,
// however many times each is recorded.
func TestDoubledRecordsStillCountAsTheRelease(t *testing.T) {
	s := testStore(t, t.Name())
	doubled := append(slices.Clone(migrations.ReplacedByBaseline), migrations.ReplacedByBaseline[0])
	recordedAs(t, s, doubled)

	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatalf("RunMigrations with a doubled record: %v", err)
	}
	if got, want := appliedMigrations(t, s), []string{migrations.BaselineName}; !slices.Equal(got, want) {
		t.Errorf("applied migrations = %v, want %v", got, want)
	}

	// Nor does a doubled record stand in for a missing one.
	s = testStore(t, t.Name()+"-short")
	short := append(slices.Clone(migrations.ReplacedByBaseline[:14]), migrations.ReplacedByBaseline[0])
	recordedAs(t, s, short)
	if err := s.RunMigrations(context.Background()); err == nil || !strings.Contains(err.Error(), "upgrade to 0.9.0") {
		t.Errorf("RunMigrations with a doubled record in place of a missing one = %v, want it refused", err)
	}
}

// Started on a database this release has taken over, 0.9.0 records its first
// two migrations, which change nothing on this schema, and fails on the
// third, which adds a column that exists. The records it leaves do not make
// the database an older one: they are removed, and the start goes on.
func TestRecordsLeftByTheReleaseAfterTheTakeoverAreRemoved(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, t.Name())
	recordedAs(t, s, append([]string{migrations.BaselineName}, migrations.ReplacedByBaseline[:2]...))
	before := dumpSchema(t, s.db)

	if err := s.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations after a 0.9.0 start on a taken-over database: %v", err)
	}
	if got, want := appliedMigrations(t, s), []string{migrations.BaselineName}; !slices.Equal(got, want) {
		t.Errorf("applied migrations = %v, want %v", got, want)
	}
	if after := dumpSchema(t, s.db); after != before {
		t.Errorf("removing the records changed the schema:\n%s", lineDiff(before, after))
	}
}

// Nearly every start finds nothing to take over. Finding that out is a read,
// and must not wait for the write lock, which a transaction takes at once
// here and an admin command may hold at that moment.
func TestTakeOverChecksWithoutTheWriteLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keyosk.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewStore(db)
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.BeginTx(ctx, nil) // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	start := time.Now()
	if err := s.takeOverReleasedMigrations(ctx); err != nil {
		t.Fatalf("the check waited for the write lock: %v", err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("the check took %v while the write lock was held", waited)
	}
}

// There is no schema to go back to before the baseline, and dropping every
// table would only lose the data. Rolling it back fails and changes nothing,
// the record included: bun unmarks a migration only once its Down has
// succeeded, so the tables cannot be left without the record that built them.
func TestBaselineCannotBeRolledBack(t *testing.T) {
	s := migratedStore(t, t.Name())
	before := dumpSchema(t, s.db)

	if _, err := newMigrator(s.db, migrations.Migrations).Rollback(context.Background()); err == nil {
		t.Error("rolling back the baseline succeeded")
	}
	if after := dumpSchema(t, s.db); after != before {
		t.Errorf("rolling back changed the schema:\n%s", lineDiff(before, after))
	}
	if got, want := appliedMigrations(t, s), []string{migrations.BaselineName}; !slices.Equal(got, want) {
		t.Errorf("applied migrations after the failed rollback = %v, want %v still", got, want)
	}
}

// bun runs a new database's migrations in name order, so one that sorts
// before the baseline would run on a database that has no tables yet.
func TestEveryMigrationSortsAfterTheBaseline(t *testing.T) {
	if first := migrations.Migrations.Sorted()[0].Name; first != migrations.BaselineName {
		t.Errorf("first migration = %s, want the baseline, %s", first, migrations.BaselineName)
	}
}
