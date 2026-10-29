package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
)

// renderExpiry renders the dashboard for d, with the fields every page needs.
func renderExpiry(t *testing.T, d dashboardData) string {
	t.Helper()
	d.User = &database.User{Name: "T", Email: "t@example.com"}
	d.APIBaseURL, d.CSRFToken = "https://gw/v1", "TOK"
	var b bytes.Buffer
	if err := parseDashboardTemplate().Execute(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A key expires at the moment it was issued or extended, plus its validity,
// and the page gives the browser that moment to show in the reader's own time
// zone. Without script the page still shows it, in UTC and saying so, whatever
// time zone the server runs in (#187).
func TestDashboardGivesTheExpiryMoment(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	out := renderExpiry(t, dashboardData{
		Lang:        i18n.EN,
		APIKey:      &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC).In(berlin)},
		ExtendUntil: time.Date(2026, 12, 25, 9, 15, 0, 0, time.UTC).In(berlin),
	})
	for _, want := range []string{
		`data-expires="2026-11-25T10:30:00Z"`,
		`<time class="local-time" datetime="2026-11-25T10:30:00Z">2026-11-25 10:30 UTC</time>`,
		`<time class="local-time" datetime="2026-12-25T09:15:00Z">2026-12-25 09:15 UTC</time>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// With less than a day left the key may expire later today or tomorrow,
// depending on the time of day and the reader's zone, so the alert names
// neither.
func TestDashboardSaysLessThanADayLeft(t *testing.T) {
	for _, c := range []struct {
		lang i18n.Lang
		want string
	}{
		{i18n.EN, "Your key expires in less than a day."},
		{i18n.DE, "Ihr Schlüssel läuft in weniger als einem Tag ab."},
	} {
		out := renderExpiry(t, dashboardData{
			Lang:          c.lang,
			APIKey:        &database.APIKey{KeyPrefix: "sk-abc", ExpiresAt: time.Now().Add(5 * time.Hour)},
			ExpiresInDays: 0,
			ExpiryUrgent:  true,
		})
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: the alert does not say %q", c.lang, c.want)
		}
	}
}
