package database

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Admin rights live on the account. Nobody the portal has seen is an admin
// until another admin, or the operator's command, sets the flag.
func TestUserAdminFlagRoundTrips(t *testing.T) {
	s := migratedStore(t, "adminflag1")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Fatal("a new account is an admin")
	}

	if err := s.SetUserAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin {
		t.Error("the flag did not persist")
	}

	if err := s.SetUserAdmin(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetUserByID(ctx, u.ID); got.IsAdmin {
		t.Error("the flag was not cleared")
	}
}

// Logging in again updates the name and address from the IdP and nothing
// else, so it must not clear the flag.
func TestLoginKeepsTheAdminFlag(t *testing.T) {
	s := migratedStore(t, "adminflag2")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}

	again, err := s.GetOrCreateUser(ctx, "sub-1", "new@uni-osnabrueck.de", "P Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsAdmin {
		t.Error("a login cleared the admin flag")
	}
	if again.Email != "new@uni-osnabrueck.de" {
		t.Errorf("email = %q, want it updated from the IdP", again.Email)
	}
}

// fileStore is a migrated store on a file opened like production's. The
// shared-cache in-memory database of migratedStore locks differently, which
// matters to tests of concurrent writes.
func fileStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewStore(db)
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// Two first logins for the same subject, such as from two tabs, both find no
// account. Both must succeed and land on the same one. A single race is lost
// only some of the time, so it runs several rounds with a fresh subject each.
func TestConcurrentFirstLogins(t *testing.T) {
	s := fileStore(t)
	ctx := context.Background()

	const rounds, n = 20, 10
	for round := range rounds {
		sub := fmt.Sprintf("sub-%d", round)
		start := make(chan struct{})
		var wg sync.WaitGroup
		ids := make([]int64, n)
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				u, err := s.GetOrCreateUser(ctx, sub, "p@uni-osnabrueck.de", "P")
				errs[i] = err
				if err == nil {
					ids[i] = u.ID
				}
			}()
		}
		close(start)
		wg.Wait()
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("round %d, login %d: %v", round, i, errs[i])
			}
			if ids[i] != ids[0] {
				t.Fatalf("round %d: login %d got account %d, login 0 got %d", round, i, ids[i], ids[0])
			}
		}
	}
}

// A login whose update fails must report it, not carry on to the insert.
// That insert would hit the same failure through its DO UPDATE, so only the
// message tells the two apart.
func TestLoginReportsAFailedUpdate(t *testing.T) {
	s := migratedStore(t, "adminflag-update")
	ctx := context.Background()

	if _, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER users_no_update BEFORE UPDATE ON users
		BEGIN SELECT RAISE(ABORT, 'update refused'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err == nil || !strings.Contains(err.Error(), "update user") {
		t.Fatalf("login = %v, want the update's error", err)
	}
}

// A rollback to an older release leaves columns in users that its User lacks.
// Logins must keep working, both the first one and later ones.
func TestLoginIgnoresUnknownColumns(t *testing.T) {
	s := migratedStore(t, "adminflag-columns")
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN added_later TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P"); err != nil {
		t.Fatalf("first login: %v", err)
	}
	if _, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P"); err != nil {
		t.Fatalf("second login: %v", err)
	}
}

// A login by someone who has an account must not use up an id, or ids would
// grow with every login.
func TestLoginKeepsIDsDense(t *testing.T) {
	s := migratedStore(t, "adminflag-ids")
	ctx := context.Background()

	for range 3 {
		if _, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.GetOrCreateUser(ctx, "sub-2", "q@uni-osnabrueck.de", "Q")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID+1 {
		t.Errorf("second account got id %d, want %d", second.ID, first.ID+1)
	}
}

// ListAdmins is the Admins tab, oldest account first; CountAdmins is the
// startup check that warns when nobody can reach the panel.
func TestListAndCountAdmins(t *testing.T) {
	s := migratedStore(t, "adminflag3")
	ctx := context.Background()

	n, err := s.CountAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("CountAdmins = %d on an empty store, want 0", n)
	}

	var ids []int64
	for _, sub := range []string{"a", "b", "c"} {
		u, err := s.GetOrCreateUser(ctx, "sub-"+sub, sub+"@uni-osnabrueck.de", sub)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	for _, id := range []int64{ids[2], ids[0]} {
		if err := s.SetUserAdmin(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}

	admins, err := s.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 2 || admins[0].ID != ids[0] || admins[1].ID != ids[2] {
		t.Errorf("ListAdmins = %+v, want accounts a then c", admins)
	}
	if n, _ = s.CountAdmins(ctx); n != 2 {
		t.Errorf("CountAdmins = %d, want 2", n)
	}
}
