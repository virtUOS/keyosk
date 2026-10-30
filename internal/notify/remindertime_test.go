package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
)

func berlinReminder(t *testing.T) *Reminder {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	r := NewReminder(nil, nil, "https://portal", nil)
	r.loc = loc
	return r
}

// The mail names the moment the key expires in the server's time zone, and
// the zone with it: it cannot know the reader's, and a time without its zone
// leaves them to guess (#187).
func TestReminderMailNamesTheMomentAndItsZone(t *testing.T) {
	k := database.ExpiringKey{
		APIKey: database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Date(2026, 11, 25, 22, 30, 0, 0, time.UTC)},
		Email:  "a@uni-osnabrueck.de",
	}
	m := berlinReminder(t).message(k, 3)
	for _, want := range []string{"am 2026-11-25 um 23:30 (CET)", "on 2026-11-25 at 23:30 CET"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("the mail does not say %q:\n%s", want, m.Body)
		}
	}
}

// A key with less than a day left may expire later today or tomorrow, so the
// last mail names neither.
func TestReminderMailSaysWithinADay(t *testing.T) {
	k := database.ExpiringKey{
		APIKey: database.APIKey{KeyPrefix: "sk-a", ExpiresAt: time.Now().Add(5 * time.Hour)},
		Email:  "a@uni-osnabrueck.de",
	}
	m := berlinReminder(t).message(k, 1)
	for _, want := range []string{"innerhalb eines Tages", "within a day"} {
		if !strings.Contains(m.Subject, want) || !strings.Contains(m.Body, want) {
			t.Errorf("subject or body does not say %q:\n%s\n%s", want, m.Subject, m.Body)
		}
	}
	for _, wrong := range []string{"morgen", "tomorrow"} {
		if strings.Contains(m.Subject+m.Body, wrong) {
			t.Errorf("the mail says %q", wrong)
		}
	}
}
