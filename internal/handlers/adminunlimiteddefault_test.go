package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/database"
)

// The seeded default profile has no quota, so everyone without a profile of
// their own can spend without a cap. The panel says so until the default has
// a quota.
func TestPanelWarnsWhileTheDefaultIsUnlimited(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "alunlimited")
	ctx := context.Background()

	body, _ := renderPanel(t, a, token)
	if !strings.Contains(body, `id="unlimited-default"`) {
		t.Fatal("panel does not warn about the unlimited default profile")
	}

	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateProfile(ctx, def, []database.ProfileQuota{{Budget: 5, Period: "30d"}}); err != nil {
		t.Fatal(err)
	}
	body, _ = renderPanel(t, a, token)
	if strings.Contains(body, `id="unlimited-default"`) {
		t.Error("panel still warns after the default profile got a quota")
	}
}
