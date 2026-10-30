package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/metrics"
)

// DefaultThresholds are how many days before expiry a user is warned.
var DefaultThresholds = []int{14, 3, 1}

// relevantShare is how much of a key's validity a threshold may span and still
// be worth a mail. Someone who just created or extended a 14-day key has seen
// the date and needs no "expires in 14 days" notice; they get the 3- and 1-day
// ones. The validity counts from when the date was last set.
const relevantShare = 0.8

// recordTimeout bounds recording a sent notice, which outlives a shutdown. It
// must stay within the shutdown grace in main.go, which waits for it.
const recordTimeout = 10 * time.Second

// Reminder warns users whose keys are close to expiring.
type Reminder struct {
	store    *database.Store
	notifier Notifier
	frontend string
	// thresholds are sorted narrowest first, which Run relies on.
	thresholds []int
	// loc is the time zone the mails give the expiry in: the server's, set
	// with TZ. A mail cannot know its reader's.
	loc *time.Location
}

func NewReminder(store *database.Store, n Notifier, frontendURL string, thresholds []int) *Reminder {
	if len(thresholds) == 0 {
		thresholds = DefaultThresholds
	}
	return &Reminder{store: store, notifier: n, frontend: frontendURL,
		thresholds: slices.Sorted(slices.Values(thresholds)), loc: time.Local}
}

// Run sends any notices due now. It is safe to call repeatedly: a notice is
// recorded per (key, threshold) and skipped thereafter. A cancelled ctx ends
// the run with its error; the notices left over are not failures.
//
// A key gets at most one mail per run. Thresholds are handled narrowest first,
// and a recorded notice also covers every wider one, so a key inside several
// windows, for instance after downtime, gets only the most urgent notice.
func (r *Reminder) Run(ctx context.Context) error {
	// Keys whose notice failed or was not recorded this run. A wider notice
	// must not go out in its place if the relay recovers before the next
	// threshold.
	failed := map[int64]bool{}
	for _, days := range r.thresholds {
		keys, err := r.store.KeysExpiringWithin(ctx, days)
		if err != nil {
			return fmt.Errorf("find keys expiring within %dd: %w", days, err)
		}
		for _, k := range keys {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if failed[k.ID] || !Relevant(&k.APIKey, days) {
				continue
			}
			msg := r.message(k, days)
			err := r.notifier.Notify(ctx, msg)
			switch {
			case errors.Is(err, ErrRejected):
				// The relay would refuse it again on every run until the key
				// expires, so record it as done.
				slog.Warn("expiry notice rejected by the relay, not retrying",
					"email", k.Email, "key_prefix", k.KeyPrefix, "days", days, "err", err)
			case err != nil && ctx.Err() != nil:
				// Cut off by a shutdown. A mail whose send completed is
				// recorded below. One cut off while the relay confirmed it
				// cannot be told apart from one that did not go out, so the
				// next run may send it again.
				return ctx.Err()
			case err != nil:
				// Leave the notice unrecorded so the next run retries.
				failed[k.ID] = true
				slog.Error("send expiry notice",
					"email", k.Email, "key_prefix", k.KeyPrefix, "days", days, "err", err)
				metrics.JobItemFailed(metrics.JobExpiryReminder)
				continue
			}
			// The wider notices are stale now; without a record a later
			// threshold in this run would mail the key again. The mail is
			// out, so a shutdown must not cut the record short and have the
			// next run send it again.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
			err = r.store.MarkExpiryNoticeSent(rctx, k.ID, days)
			cancel()
			if err != nil {
				failed[k.ID] = true
				slog.Error("record expiry notice", "key_id", k.ID, "err", err)
				metrics.JobItemFailed(metrics.JobExpiryReminder)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return nil
}

// Relevant reports whether a notice days before expiry is worth sending for
// k: the threshold must be under relevantShare of the key's validity. The
// dashboard uses the same rule, so it nags only when a mail would go out.
func Relevant(k *database.APIKey, days int) bool {
	validity := k.ExpiresAt.Sub(k.ExpirySetAt)
	lead := time.Duration(days) * 24 * time.Hour
	return float64(lead) < relevantShare*float64(validity)
}

// Start runs the reminder on an interval until ctx is cancelled.
func (r *Reminder) Start(ctx context.Context, every time.Duration) {
	metrics.JobStarted(metrics.JobExpiryReminder, every)
	run := func() {
		err := r.Run(ctx)
		if ctx.Err() != nil {
			// Shutting down: a run cut short is neither an error nor a success.
			return
		}
		if err != nil {
			slog.Error("expiry reminder", "err", err)
			return
		}
		metrics.JobSucceeded(metrics.JobExpiryReminder)
	}
	// Run once at startup so a restart does not delay overdue notices.
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			run()
		case <-ctx.Done():
			return
		}
	}
}

// message builds the expiry notice. It is bilingual, German first: the portal
// is German by default, but nothing records a user's language, so the mail
// cannot pick one. Both halves carry the date and the link, so either is
// enough on its own.
func (r *Reminder) message(k database.ExpiringKey, days int) Message {
	whenDE := "in " + pluralDaysDE(days)
	whenEN := "in " + pluralDaysEN(days)
	if days == 1 {
		// Less than a day left may end later today or tomorrow, depending on
		// the time and the reader's zone, so the mail names neither.
		whenDE, whenEN = "innerhalb eines Tages", "within a day"
	}

	name := k.Name
	if name == "" {
		name = k.Email
	}
	// The moment the key expires, in the server's zone and naming it, so
	// the reader can tell what it is in theirs.
	at := k.ExpiresAt.In(r.loc)
	date, clock, zone := at.Format("2006-01-02"), at.Format("15:04"), at.Format("MST")

	body := fmt.Sprintf(`Hallo %s,

Ihr KI-API-Schlüssel (%s…) läuft %s ab, am %s um %s (%s).

Sie können ihn mit einem Klick um einen vollen Zeitraum verlängern oder
einen neuen erzeugen:

  %s

Wenn Sie den Schlüssel nicht mehr benötigen, können Sie diese Nachricht
ignorieren; er wird von selbst ungültig.

---

Hello %s,

your AI API key (%s…) expires %s, on %s at %s %s.

You can extend it for a full period with one click, or generate a
replacement, at:

  %s

If you no longer need the key you can ignore this message; it will stop
working on its own.
`, name, k.KeyPrefix, whenDE, date, clock, zone, r.frontend,
		name, k.KeyPrefix, whenEN, date, clock, zone, r.frontend)

	return Message{
		To:      k.Email,
		Subject: fmt.Sprintf("Ihr KI-API-Schlüssel läuft %s ab / Your AI API key expires %s", whenDE, whenEN),
		Body:    body,
	}
}

func pluralDaysDE(d int) string {
	if d == 1 {
		return "1 Tag"
	}
	return fmt.Sprintf("%d Tagen", d)
}

func pluralDaysEN(d int) string {
	if d == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", d)
}
