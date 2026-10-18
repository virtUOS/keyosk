package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Record when each key's current expiry date was set.
//
// The expiry reminder skips a threshold that is not well inside the key's
// validity: a user who just created or extended a 14-day key has seen the date
// and needs no "expires in 14 days" mail. The validity starts when the date was
// last set, which created_at does not track, since extending leaves it alone.
//
// Existing keys start from created_at. For a key extended before this
// migration that makes the validity look longer than it is, so it may get one
// reminder the new rule would have skipped, which is what it got before.
func init() {
	Migrations.MustRegister(inTx(func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE api_keys ADD COLUMN expiry_set_at TIMESTAMP`); err != nil {
			return fmt.Errorf("add expiry_set_at: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE api_keys SET expiry_set_at = created_at`); err != nil {
			return fmt.Errorf("backfill expiry_set_at: %w", err)
		}
		return nil
	}), inTx(func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE api_keys DROP COLUMN expiry_set_at`); err != nil {
			return fmt.Errorf("drop expiry_set_at: %w", err)
		}
		return nil
	}))
}
