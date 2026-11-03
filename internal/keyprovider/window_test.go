package keyprovider

import (
	"testing"
	"time"
)

// A window of days days ends today, so it starts days-1 back: the gateway's
// start date and the chart's first bar are both inclusive. The days are UTC
// days, whatever zone now is in.
func TestWindowStartEndsTheWindowToday(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	for _, tc := range []struct {
		now  time.Time
		days int
		want string
	}{
		{time.Date(2026, 11, 3, 15, 30, 0, 0, cet), 1, "2026-11-03"},
		{time.Date(2026, 11, 3, 15, 30, 0, 0, cet), 30, "2026-10-05"},
		{time.Date(2026, 11, 3, 15, 30, 0, 0, cet), 0, "2026-11-03"},
		{time.Date(2026, 11, 3, 0, 30, 0, 0, cet), 1, "2026-11-02"}, // still the 2nd in UTC
	} {
		if got := WindowStart(tc.now, tc.days).Format("2006-01-02"); got != tc.want {
			t.Errorf("%d days ending %v: start %s, want %s", tc.days, tc.now, got, tc.want)
		}
	}
	if got := WindowStart(time.Date(2026, 11, 3, 15, 30, 0, 0, cet), 30); !got.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("start = %v, want midnight UTC", got)
	}
}
