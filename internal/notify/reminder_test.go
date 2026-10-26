package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/metrics/metricstest"
)

// recorder captures messages instead of sending them.
type recorder struct {
	mu   sync.Mutex
	sent []Message
	err  error
	// fail is how many of the next calls are refused, as a relay that
	// recovers would.
	fail int
	// onNotify, if set, runs before each call; a test uses it to cancel the
	// run's context mid-way, as a shutdown does.
	onNotify func()
}

func (r *recorder) Notify(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.onNotify != nil {
		r.onNotify()
	}
	if r.err != nil {
		return r.err
	}
	if r.fail > 0 {
		r.fail--
		return errors.New("relay down")
	}
	r.sent = append(r.sent, m)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

// setup opens an in-memory database and returns the store and the handle it
// wraps, for the raw updates the store's API does not offer.
func setup(t *testing.T, name string) (*database.Store, *bun.DB) {
	t.Helper()
	sqldb, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })
	s := database.NewStore(db)
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, db
}

// addKey creates a user with a 30-day key expiring in the given number of
// days, long enough for every default threshold to apply.
func addKey(t *testing.T, s *database.Store, sub, email string, inDays int) *database.APIKey {
	t.Helper()
	ctx := context.Background()
	u, err := s.GetOrCreateUser(ctx, sub, email, "Test User")
	if err != nil {
		t.Fatal(err)
	}
	k := &database.APIKey{
		UserID: u.ID, KeyRef: "sk-" + sub, KeyPrefix: "sk-" + sub,
		ExpiresAt: time.Now().AddDate(0, 0, inDays),
	}
	if err := s.ReplaceAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	// Set as of inDays before its expiry, so that a 30-day validity has run
	// down to inDays left.
	if err := s.SetAPIKeyExpiry(ctx, k.ID, k.ExpiresAt.AddDate(0, 0, -30), k.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	return k
}

// setRemaining moves a key to the point in its life where left remains of a
// validity of validDays, as if time had passed since its expiry was set.
func setRemaining(t *testing.T, db *bun.DB, keyID int64, left time.Duration, validDays int) {
	t.Helper()
	expires := time.Now().Add(left)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE api_keys SET expires_at = ?, expiry_set_at = ? WHERE id = ?`,
		expires, expires.AddDate(0, 0, -validDays), keyID); err != nil {
		t.Fatal(err)
	}
}

// pending reports which of the default thresholds the reminder would still
// consider for the key, that is, have no notice recorded.
func pending(t *testing.T, s *database.Store, keyID int64) []int {
	t.Helper()
	var due []int
	for _, d := range DefaultThresholds {
		keys, err := s.KeysExpiringWithin(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if k.ID == keyID {
				due = append(due, d)
			}
		}
	}
	return due
}

func TestReminderWarnsKeysInsideWindow(t *testing.T) {
	s, _ := setup(t, "rem1")
	addKey(t, s, "soon", "soon@uni.de", 2)    // inside 3d and 14d
	addKey(t, s, "later", "later@uni.de", 40) // outside every window

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.count() != 1 {
		t.Fatalf("sent %d messages, want 1 (3d for the soon key)", rec.count())
	}
	for _, m := range rec.sent {
		if m.To != "soon@uni.de" {
			t.Errorf("unexpected recipient %s", m.To)
		}
		if !strings.Contains(m.Body, "https://portal") {
			t.Error("message lacks the portal link")
		}
	}
}

// The whole point of the notices table: repeated runs must not re-mail.
func TestReminderDoesNotRepeat(t *testing.T) {
	s, _ := setup(t, "rem2")
	addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first := rec.count()
	for i := 0; i < 3; i++ {
		if err := r.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if rec.count() != first {
		t.Fatalf("re-sent notices: %d after repeated runs, want %d", rec.count(), first)
	}
}

// Extending a key moves its expiry date, so the warnings for the old date must
// not suppress the warnings for the new one.
func TestReminderWarnsAgainAfterExtension(t *testing.T) {
	s, db := setup(t, "rem7")
	k := addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d messages before the extension, want 1 (3d)", rec.count())
	}

	if err := s.UpdateAPIKeyExpiry(ctx, k.ID, time.Now().AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	// The new period runs down to 2 days left.
	setRemaining(t, db, k.ID, 2*24*time.Hour, 30)
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 2 {
		t.Fatalf("sent %d messages after the extension, want 2 (3d again)", rec.count())
	}
}

// Someone who has just extended a key has seen the new date and needs no
// notice about it, although the key is inside the 14-day window.
func TestReminderSkipsNoticeRightAfterExtension(t *testing.T) {
	s, _ := setup(t, "rem8")
	k := addKey(t, s, "u1", "u1@uni.de", 2)
	ctx := context.Background()
	if err := s.UpdateAPIKeyExpiry(ctx, k.ID, time.Now().AddDate(0, 0, 14)); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatalf("sent %d messages right after the extension, want none", rec.count())
	}
}

// A threshold applies only when it is well inside the key's validity: a 14-day
// key gets the 3- and 1-day notices, a 30-day key all three, each once.
func TestReminderSendsThresholdsTheValidityAllows(t *testing.T) {
	for _, tc := range []struct {
		valid int
		want  []string
	}{
		{14, []string{"in 3 days", "tomorrow"}},
		{30, []string{"in 14 days", "in 3 days", "tomorrow"}},
	} {
		t.Run(fmt.Sprintf("%dd", tc.valid), func(t *testing.T) {
			s, db := setup(t, fmt.Sprintf("rem9-%d", tc.valid))
			k := addKey(t, s, "u1", "u1@uni.de", tc.valid)
			rec := &recorder{}
			r := NewReminder(s, rec, "https://portal", nil)

			// Run at creation and at each threshold, as time passes.
			for _, left := range []time.Duration{
				time.Duration(tc.valid) * 24 * time.Hour,
				13 * 24 * time.Hour, 2 * 24 * time.Hour, 12 * time.Hour,
			} {
				setRemaining(t, db, k.ID, left, tc.valid)
				if err := r.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

			var got []string
			for _, m := range rec.sent {
				for _, w := range []string{"in 14 days", "in 3 days", "tomorrow"} {
					if strings.Contains(m.Subject, w) {
						got = append(got, w)
					}
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("notices = %q, want %q", got, tc.want)
			}
		})
	}
}

// A key inside several windows at once, for instance after downtime, gets
// only the most urgent notice, and the wider ones are not sent later.
func TestReminderSendsOnlyTheNarrowestDueNotice(t *testing.T) {
	s, db := setup(t, "rem10")
	k := addKey(t, s, "u1", "u1@uni.de", 30)
	setRemaining(t, db, k.ID, 12*time.Hour, 30)

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := r.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d messages, want 1", rec.count())
	}
	if !strings.Contains(rec.sent[0].Subject, "tomorrow") {
		t.Errorf("subject = %q, want the 1-day notice", rec.sent[0].Subject)
	}
	if p := pending(t, s, k.ID); len(p) != 0 {
		t.Errorf("notices still pending: %v, want all recorded", p)
	}
}

// When the most urgent notice fails, the wider ones must not go out instead,
// and nothing is recorded, so the next run retries the urgent one.
func TestReminderRetriesTheNarrowestNoticeAfterFailure(t *testing.T) {
	s, db := setup(t, "rem11")
	k := addKey(t, s, "u1", "u1@uni.de", 30)
	setRemaining(t, db, k.ID, 12*time.Hour, 30)

	rec := &recorder{err: errors.New("relay down")}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if p := pending(t, s, k.ID); !slices.Equal(p, []int{14, 3, 1}) {
		t.Errorf("pending after failure = %v, want all three", p)
	}

	rec.err = nil
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 || !strings.Contains(rec.sent[0].Subject, "tomorrow") {
		t.Fatalf("sent %+v after recovery, want the 1-day notice only", rec.sent)
	}
}

// Already-expired keys are not worth a warning.
func TestReminderSkipsExpiredKeys(t *testing.T) {
	s, _ := setup(t, "rem3")
	addKey(t, s, "gone", "gone@uni.de", -5)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatalf("warned about an expired key: %+v", rec.sent)
	}
}

// A delivery failure must not record the notice, so the next run retries.
func TestReminderRetriesAfterFailure(t *testing.T) {
	s, _ := setup(t, "rem4")
	addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{err: errors.New("relay down")}
	r := NewReminder(s, rec, "https://portal", []int{3})
	ctx := context.Background()

	failures := func() float64 {
		return metricstest.Counter(t, "keyosk_job_item_failures_total", `task="expiry_reminder"`)
	}
	before := failures()
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatal("recorder should have captured nothing while failing")
	}
	if got := failures() - before; got != 1 {
		t.Errorf("item failures rose by %v after a failed notice, want 1", got)
	}

	// Relay recovers; the notice must still be pending.
	rec.err = nil
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("notice not retried after failure: sent %d", rec.count())
	}
}

// A shutdown mid-run ends the run with the context's error and counts none of
// the notices it cut off as failures; a restart must not look like a relay
// that keeps refusing.
func TestReminderStopsOnCancelWithoutCountingFailures(t *testing.T) {
	s, _ := setup(t, "rem-cancel")
	addKey(t, s, "u1", "u1@uni.de", 2)
	addKey(t, s, "u2", "u2@uni.de", 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &recorder{err: errors.New("connection closed"), onNotify: cancel}
	before := metricstest.Counter(t, "keyosk_job_item_failures_total", `task="expiry_reminder"`)
	err := NewReminder(s, rec, "https://portal", []int{3}).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	if got := metricstest.Counter(t, "keyosk_job_item_failures_total", `task="expiry_reminder"`) - before; got != 0 {
		t.Errorf("item failures rose by %v during shutdown, want 0", got)
	}
	if rec.count() != 0 {
		t.Errorf("sent %d notices after the cancel, want 0", rec.count())
	}
}

// A shutdown right after a notice went out must still record it, or the next
// run mails the key again, and end the run with the context's error.
func TestReminderRecordsASentNoticeDespiteCancel(t *testing.T) {
	s, db := setup(t, "rem-cancel-sent")
	k := addKey(t, s, "u1", "u1@uni.de", 30)
	setRemaining(t, db, k.ID, 12*time.Hour, 30)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &recorder{onNotify: cancel}
	err := NewReminder(s, rec, "https://portal", nil).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d messages, want 1", rec.count())
	}
	if p := pending(t, s, k.ID); len(p) != 0 {
		t.Errorf("notices still pending after the cancel: %v, want all recorded", p)
	}
}

// A recipient the relay refuses permanently is recorded, so later runs do not
// try it again until the key expires.
func TestReminderDoesNotRetryARejectedRecipient(t *testing.T) {
	s, _ := setup(t, "rem-rejected")
	addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{err: fmt.Errorf("send mail to u1@uni.de: %w: 550 user unknown", ErrRejected)}
	r := NewReminder(s, rec, "https://portal", []int{3})
	ctx := context.Background()

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Were the notice still pending, this run would deliver it.
	rec.err = nil
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatalf("rejected notice was retried: sent %d", rec.count())
	}
}

func TestReminderMessageWording(t *testing.T) {
	s, _ := setup(t, "rem5")
	addKey(t, s, "u1", "u1@uni.de", 1)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", []int{1}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d, want 1", rec.count())
	}
	m := rec.sent[0]
	if !strings.Contains(m.Subject, "tomorrow") {
		t.Errorf("subject = %q, want it to say tomorrow", m.Subject)
	}
	if !strings.Contains(m.Body, "Test User") {
		t.Error("body does not address the user by name")
	}
}

// Discard must not error, so an unconfigured deployment still runs.
func TestDiscardNotifier(t *testing.T) {
	if err := (Discard{}).Notify(context.Background(), Message{To: "a@b.c"}); err != nil {
		t.Fatalf("Discard returned %v", err)
	}
}

// The portal is German by default, so notices must not arrive English-only.
// There is no per-user language on record, so the mail carries both, German
// first to match the UI default. See issue #6.
func TestReminderMessageIsBilingual(t *testing.T) {
	s, _ := setup(t, "rem6")
	addKey(t, s, "u1", "u1@uni.de", 1)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", []int{1}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := rec.sent[0]

	for _, want := range []string{"Ihr KI-API-Schlüssel", "läuft", "verlängern"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing German text %q", want)
		}
	}
	for _, want := range []string{"AI API key", "expires", "extend"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing English text %q", want)
		}
	}
	if !strings.Contains(m.Subject, "läuft") || !strings.Contains(m.Subject, "expires") {
		t.Errorf("subject = %q, want both languages", m.Subject)
	}
	// Both halves must still carry the facts the user needs to act.
	if strings.Count(m.Body, "https://portal") < 2 {
		t.Error("portal link should appear in both halves")
	}
}

// When the most urgent notice fails and the relay recovers within the same
// run, no wider notice goes out in its place; the next run sends the urgent
// one.
func TestReminderSendsNoWiderNoticeInPlaceOfAFailedOne(t *testing.T) {
	s, db := setup(t, "rem12")
	k := addKey(t, s, "u1", "u1@uni.de", 30)
	setRemaining(t, db, k.ID, 12*time.Hour, 30)

	rec := &recorder{fail: 1}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatalf("sent %+v in the run where the 1-day notice failed, want none", rec.sent)
	}
	if p := pending(t, s, k.ID); !slices.Equal(p, []int{14, 3, 1}) {
		t.Errorf("pending after failure = %v, want all three", p)
	}

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 || !strings.Contains(rec.sent[0].Subject, "tomorrow") {
		t.Fatalf("sent %+v after recovery, want the 1-day notice only", rec.sent)
	}
	if p := pending(t, s, k.ID); len(p) != 0 {
		t.Errorf("notices still pending: %v, want all recorded", p)
	}
}
