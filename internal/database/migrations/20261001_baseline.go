package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// BaselineName is the baseline's name. A migration added after it has to sort
// after it, since bun runs a new database's migrations in name order.
const BaselineName = "20261001"

// ReplacedByBaseline names the migrations up to 0.9.0, which the baseline
// replaced. A database 0.9.0 has run on has all of them recorded and already
// has the schema the baseline builds, so Store.RunMigrations records the
// baseline in their place instead of running it.
var ReplacedByBaseline = []string{
	"20240001", "20240002", "20240003", "20240004", "20240005",
	"20240006", "20240007", "20240008", "20240009", "20240010",
	"20240011", "20240012", "20240013", "20240014", "20240015",
}

// The schema as 0.9.0 left it, created at once rather than in the fifteen
// steps that built it. The test against testdata/schema-v0.9.0.txt holds it
// to that: same columns in the same order, same constraints and indexes. The
// column types mix DATETIME and TIMESTAMP as the steps that added them did;
// SQLite treats both alike.
//
// The constraints are what keeps the data right when handler code fails
// halfway or two requests race:
//
//   - idx_api_keys_user_id allows one key per user. Duplicates were keys a
//     failed rotation left live in LiteLLM with nothing pointing at them.
//   - idx_profiles_single_default is partial, so it allows any number of
//     profiles that are not the default and at most one that is.
//   - idx_expiry_notice_once sends each reminder at most once per key and
//     threshold, even when two runs of the job overlap.
//   - profile_quotas holds one window per period, and goes with its profile.
//
// audit_events copies the actor's and the subject's address rather than
// referencing the user, so the history of a key outlives the account.
// users.profile_after_expiry has no foreign key: DeleteProfile moves anyone
// due to land on the deleted profile to the default instead.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		for _, stmt := range []string{
			`CREATE TABLE profiles (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				name              TEXT    NOT NULL UNIQUE,
				description       TEXT    NOT NULL DEFAULT '',
				models            TEXT    NOT NULL DEFAULT '[]',
				tpm_limit         INTEGER,
				rpm_limit         INTEGER,
				max_budget        REAL,
				budget_duration   TEXT,
				is_default        INTEGER NOT NULL DEFAULT 0,
				created_at        DATETIME NOT NULL,
				updated_at        DATETIME NOT NULL,
				key_duration_days INTEGER NOT NULL DEFAULT 0,
				limits_rev        INTEGER NOT NULL DEFAULT 1
			)`,
			`CREATE UNIQUE INDEX idx_profiles_single_default
				ON profiles (is_default) WHERE is_default <> 0`,

			`CREATE TABLE profile_quotas (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
				period     TEXT    NOT NULL,
				budget     REAL    NOT NULL DEFAULT 0,
				UNIQUE (profile_id, period)
			)`,

			`CREATE TABLE users (
				id                   INTEGER PRIMARY KEY AUTOINCREMENT,
				oidc_sub             TEXT    NOT NULL UNIQUE,
				email                TEXT    NOT NULL,
				name                 TEXT    NOT NULL DEFAULT '',
				profile_id           INTEGER REFERENCES profiles(id),
				created_at           DATETIME NOT NULL,
				updated_at           DATETIME NOT NULL,
				profile_expires_at   TIMESTAMP,
				profile_after_expiry INTEGER,
				revoke_key_at_expiry BOOLEAN NOT NULL DEFAULT 0,
				is_admin             BOOLEAN NOT NULL DEFAULT 0
			)`,
			// The profile expiry job looks for passed deadlines every 15
			// minutes.
			`CREATE INDEX idx_users_profile_expires_at ON users (profile_expires_at)`,

			`CREATE TABLE api_keys (
				id                 INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				key_ref            TEXT    NOT NULL,
				key_prefix         TEXT    NOT NULL,
				expires_at         DATETIME NOT NULL,
				created_at         DATETIME NOT NULL,
				synced_profile_id  INTEGER,
				synced_rev         INTEGER,
				sync_error         TEXT    NOT NULL DEFAULT '',
				sync_failed_at     TIMESTAMP,
				synced_owner_name  TEXT,
				synced_owner_email TEXT,
				missing_since      TIMESTAMP,
				expiry_set_at      TIMESTAMP
			)`,
			`CREATE UNIQUE INDEX idx_api_keys_user_id ON api_keys (user_id)`,

			`CREATE TABLE sessions (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				token      TEXT    NOT NULL UNIQUE,
				id_token   TEXT    NOT NULL,
				expires_at DATETIME NOT NULL,
				created_at DATETIME NOT NULL
			)`,
			`CREATE INDEX idx_sessions_expires_at ON sessions (expires_at)`,

			`CREATE TABLE audit_events (
				id            INTEGER PRIMARY KEY AUTOINCREMENT,
				created_at    DATETIME NOT NULL,
				action        TEXT     NOT NULL,
				actor_email   TEXT     NOT NULL,
				subject_email TEXT     NOT NULL,
				subject_id    INTEGER,
				detail        TEXT     NOT NULL DEFAULT ''
			)`,
			// The panel lists newest first, and a person's history is the
			// common filter.
			`CREATE INDEX idx_audit_created_at ON audit_events (created_at DESC)`,
			`CREATE INDEX idx_audit_subject ON audit_events (subject_id)`,

			`CREATE TABLE expiry_notices (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				api_key_id  INTEGER NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
				days_before INTEGER NOT NULL,
				sent_at     DATETIME NOT NULL
			)`,
			`CREATE UNIQUE INDEX idx_expiry_notice_once ON expiry_notices (api_key_id, days_before)`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create the schema: %w", err)
			}
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		// Dropping every table would lose the data and leave a database that
		// no release can use.
		return errors.New("the baseline is the first schema; there is none to roll back to")
	}))
}
