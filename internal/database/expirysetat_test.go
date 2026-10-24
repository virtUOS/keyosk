package database

import (
	"context"
	"testing"
	"time"
)

// Keys from before the column existed count their validity from creation,
// the best record there is of when their date was set.
func TestMigrationStartsExpirySetAtAtCreation(t *testing.T) {
	s := testStore(t, "esa1")
	ctx := context.Background()
	migrateBefore(t, s, "20240015")

	created := time.Now().AddDate(0, 0, -10).UTC().Truncate(time.Second)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, oidc_sub, email, name, created_at, updated_at)
		  VALUES (1,'s1','a@b.c','A',?,?);
		INSERT INTO api_keys (user_id, key_ref, key_prefix, expires_at, created_at)
		  VALUES (1,'ref','sk-a',?,?);
	`, created, created, created.AddDate(0, 0, 30), created); err != nil {
		t.Fatal(err)
	}

	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !k.ExpirySetAt.Equal(created) {
		t.Errorf("expiry_set_at = %v, want created_at %v", k.ExpirySetAt, created)
	}
}

// Creating a key sets the start of its validity, extending moves it to now,
// and neither touches another key.
func TestExpirySetAtFollowsCreateAndExtend(t *testing.T) {
	s := migratedStore(t, "esa2")
	ctx := context.Background()
	u1, _ := s.GetOrCreateUser(ctx, "s1", "a@b.c", "A")
	u2, _ := s.GetOrCreateUser(ctx, "s2", "c@d.e", "B")

	exp := time.Now().AddDate(0, 0, 14)
	k1 := &APIKey{UserID: u1.ID, KeyRef: "sk-u1", KeyPrefix: "sk-u1", ExpiresAt: exp}
	k2 := &APIKey{UserID: u2.ID, KeyRef: "sk-u2", KeyPrefix: "sk-u2", ExpiresAt: exp}
	for _, k := range []*APIKey{k1, k2} {
		if err := s.ReplaceAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if !k1.ExpirySetAt.Equal(k1.CreatedAt) {
		t.Errorf("expiry_set_at = %v, want created_at %v", k1.ExpirySetAt, k1.CreatedAt)
	}

	// Pretend both keys were created a week ago.
	weekAgo := time.Now().AddDate(0, 0, -7).UTC().Truncate(time.Second)
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET expiry_set_at = ?`, weekAgo); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := s.UpdateAPIKeyExpiry(ctx, k1.ID, time.Now().AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}

	got1, err := s.GetAPIKeyByUser(ctx, u1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.ExpirySetAt.Before(before) {
		t.Errorf("extended key: expiry_set_at = %v, want about now", got1.ExpirySetAt)
	}
	got2, err := s.GetAPIKeyByUser(ctx, u2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got2.ExpirySetAt.Equal(weekAgo) {
		t.Errorf("other key: expiry_set_at = %v, want %v", got2.ExpirySetAt, weekAgo)
	}
}

// Recording a notice records the superseded ones with it and leaves ones
// already recorded alone.
func TestMarkExpiryNoticeSentRecordsSupersededOnes(t *testing.T) {
	s := migratedStore(t, "esa3")
	ctx := context.Background()
	u, _ := s.GetOrCreateUser(ctx, "s1", "a@b.c", "A")
	k := &APIKey{UserID: u.ID, KeyRef: "sk-u1", KeyPrefix: "sk-u1",
		ExpiresAt: time.Now().Add(12 * time.Hour)}
	if err := s.ReplaceAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkExpiryNoticeSent(ctx, k.ID, 14); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkExpiryNoticeSent(ctx, k.ID, 3, 14); err != nil {
		t.Fatalf("mark with a superseded notice already recorded: %v", err)
	}
	for _, d := range []int{14, 3} {
		if rows, err := s.KeysExpiringWithin(ctx, d); err != nil || len(rows) != 0 {
			t.Errorf("%dd: expiring = %+v, err = %v; want the notice recorded", d, rows, err)
		}
	}
	if rows, err := s.KeysExpiringWithin(ctx, 1); err != nil || len(rows) != 1 {
		t.Errorf("1d: expiring = %+v, err = %v; want the key still pending", rows, err)
	}
}
