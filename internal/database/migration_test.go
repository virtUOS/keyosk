package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	_ "modernc.org/sqlite"

	"github.com/virtuos/keyosk/internal/database/migrations"
)

func testStore(t *testing.T, name string) *Store {
	t.Helper()
	sqldb, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

// A user must not be able to hold two keys once the constraint exists.
func TestUniqueKeyPerUser(t *testing.T) {
	s := testStore(t, "m1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetOrCreateUser(ctx, "sub1", "a@b.c", "A")
	if err != nil {
		t.Fatal(err)
	}
	k := &APIKey{UserID: u.ID, KeyRef: "sk-1", KeyPrefix: "sk-1", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := s.db.NewInsert().Model(k).Exec(ctx); err != nil {
		t.Fatalf("first key: %v", err)
	}
	second := &APIKey{UserID: u.ID, KeyRef: "sk-2", KeyPrefix: "sk-2", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := s.db.NewInsert().Model(second).Exec(ctx); err == nil {
		t.Fatal("second key for the same user was accepted; unique constraint missing")
	}
}

// The index is the backstop beneath CreateProfile's demote-then-insert: a raw
// INSERT that bypasses the store must still be refused. (Callers going through
// CreateProfile see the incumbent demoted instead — see
// TestCreateDefaultProfileDemotesPrevious.)
func TestSingleDefaultProfile(t *testing.T) {
	s := testStore(t, "m2")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProfile(ctx, &Profile{Name: "d1", IsDefault: true}, nil); err != nil {
		t.Fatalf("first default: %v", err)
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES ('d2', 1, ?, ?)`,
		now, now)
	if err == nil {
		t.Fatal("raw insert of a second default accepted; partial unique index missing")
	}
	// Non-default profiles must still be unrestricted.
	for _, n := range []string{"p1", "p2", "p3"} {
		if _, err := s.CreateProfile(ctx, &Profile{Name: n}, nil); err != nil {
			t.Fatalf("non-default profile %s rejected: %v", n, err)
		}
	}
}

// A migration that fails halfway must leave no trace: its statements are
// rolled back and it is not recorded, so the next start runs it again instead
// of skipping it.
func TestFailedMigrationIsNotMarkedApplied(t *testing.T) {
	s := testStore(t, "m4")
	ctx := context.Background()

	const name = "20991231000000"
	withLast := func(up func(ctx context.Context, db *bun.DB) error) *migrate.Migrations {
		ms := migrate.NewMigrations()
		for _, m := range migrations.Migrations.Sorted() {
			ms.Add(m)
		}
		ms.Add(migrate.Migration{
			Name: name,
			Up: func(ctx context.Context, m *migrate.Migrator, _ *migrate.Migration) error {
				return up(ctx, m.DB())
			},
		})
		return ms
	}
	failing := func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE half_done (id INTEGER)`); err != nil {
				return err
			}
			return errors.New("fails after the first statement")
		})
	}

	m := newMigrator(s.db, withLast(failing))
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Migrate(ctx); err == nil {
		t.Fatal("failing migration reported success")
	}
	if _, err := s.db.ExecContext(ctx, `SELECT id FROM half_done`); err == nil {
		t.Error("half_done exists; the failed migration was not rolled back")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bun_migrations WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("failed migration recorded as applied")
	}

	// Once fixed, the next run picks it up.
	fixed := func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE half_done (id INTEGER)`)
		return err
	}
	if _, err := newMigrator(s.db, withLast(fixed)).Migrate(ctx); err != nil {
		t.Fatalf("fixed migration: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `SELECT id FROM half_done`); err != nil {
		t.Errorf("fixed migration did not run: %v", err)
	}
}
