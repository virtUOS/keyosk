package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/internal/keyprovider"
)

func windowsUI(fake *keyprovider.Fake) *UI {
	return &UI{usage: newUsageCache(fake), keys: fake}
}

// A profile can hold several allowances at once, and the card shows a bar per
// window. Reporting only one would misstate what the user has left: room on a
// monthly allowance says nothing about an hourly cap that is nearly spent.
func TestUserUsageReportsEveryWindow(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {
			{Period: "1h", Used: 0.8, Limit: 1.0},
			{Period: "30d", Used: 0.00959, Limit: 1.0},
		},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(got.Windows))
	}
	if got.Windows[0].Pct != 80 {
		t.Errorf("1h window at %d%%, want 80", got.Windows[0].Pct)
	}
	if got.Windows[0].Label == "" {
		t.Error("window is missing its formatted label")
	}
}

// Only the widest window is held against the person. The shorter ones live on
// the key and start over with a new one, so the card must not present them as
// the person's allowance.
func TestUserUsageMarksShorterWindowsPerKey(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {
			{Period: "1h", Used: 0.1, Limit: 1.0},
			{Period: "24h", Used: 0.1, Limit: 2.0},
		},
	}
	fake.WindowsByOwner = map[string][]keyprovider.WindowUsage{
		"sub-1": {{Period: "30d", Used: 0.1, Limit: 5.0}},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	want := map[string]bool{"1h": true, "24h": true, "30d": false}
	if len(got.Windows) != len(want) {
		t.Fatalf("got %d windows, want %d", len(got.Windows), len(want))
	}
	for _, w := range got.Windows {
		if w.PerKey != want[w.Period] {
			t.Errorf("%s window PerKey = %v, want %v", w.Period, w.PerKey, want[w.Period])
		}
	}
	if !got.AnyPerKey() {
		t.Error("the card does not know it shows a per-key window")
	}
}

// The card takes where a window lives from the provider rather than guessing
// from the periods. A key from before owners were tracked holds every window
// itself, and its widest must not be presented as the person's allowance:
// it is as gone after a rotation as the others.
func TestUserUsageDoesNotPromoteAKeyWindowToThePerson(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-old": {
			{Period: "1h", Used: 0.1, Limit: 1.0},
			{Period: "30d", Used: 0.1, Limit: 5.0},
		},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-old"}, "sub-1", i18n.EN)

	if len(got.Windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(got.Windows))
	}
	for _, w := range got.Windows {
		if !w.PerKey {
			t.Errorf("%s window is shown as the person's, but lives on the key", w.Period)
		}
	}
}

// A single window is the person's, whatever its period.
func TestPerKeyWindowsSingleWindowIsThePersons(t *testing.T) {
	windows := []keyprovider.QuotaWindow{{Budget: 1, Period: "1h"}}
	if perKeyWindows(windows)["1h"] {
		t.Error("a profile's only window is marked per key")
	}
}

// The profile card's marker follows the provider's own split, so it cannot
// label a window as the person's that the provider puts on the key.
func TestPerKeyWindowsFollowsTheProviderSplit(t *testing.T) {
	windows := []keyprovider.QuotaWindow{
		{Budget: 1, Period: "7d"},
		{Budget: 0.1, Period: "1h"},
		{Budget: 2, Period: "30d"},
		{Budget: 0, Period: "24h"}, // no allowance, not a window
	}
	got := perKeyWindows(windows)
	want := map[string]bool{"7d": true, "1h": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, v := range want {
		if got[p] != v {
			t.Errorf("%s = %v, want %v", p, got[p], v)
		}
	}
}

// A gateway that cannot report windows must not break the card, and the card
// must say so rather than read as "no limit".
func TestUserUsageSurvivesWindowError(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsErr = errors.New("gateway down")

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 0 {
		t.Error("windows reported despite the error")
	}
	if !got.Incomplete {
		t.Error("a failed read is not reported as incomplete")
	}
	if got.KeyMissing {
		t.Error("a failed read is reported as a missing key")
	}
}

// An over-spent window renders as full rather than overflowing its bar.
func TestQuotaPctClampsAtFull(t *testing.T) {
	if got := quotaPct(2.0, 1.0); got != 100 {
		t.Errorf("quotaPct(2,1) = %d, want 100", got)
	}
	if got := quotaPct(10, 0); got != 0 {
		t.Errorf("quotaPct with no limit = %d, want 0", got)
	}
}

// Windows come back tightest first so the card reads in the order a user hits
// them, and the reset time is carried through for each.
func TestUserUsageKeepsWindowResetTimes(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).UTC()
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{
		"sk-live": {{Period: "1h", Used: 0.1, Limit: 1.0, ResetsAt: reset}},
	}

	got := windowsUI(fake).userUsage(context.Background(),
		&database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)

	if len(got.Windows) != 1 || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("reset time not carried through: %+v", got.Windows)
	}
}
