package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/internal/keyprovider"
)

// slowReporter stands in for a gateway that does not answer: History for the
// slow owner, and Windows when slowWindows is set, block until the reporter
// is released or the context ends. Everything else answers from the fake at
// once, unless the context has ended already: then, as with the gateway
// client, the read fails at once. With panics set, History panics, as a bug
// in decoding the gateway's answer would. calls counts the History reads;
// waiting is how many reads are blocked right now, so a test can tell that a
// read is under way.
type slowReporter struct {
	*keyprovider.Fake
	slow        string
	slowWindows bool
	panics      bool
	released    chan struct{}
	once        sync.Once
	calls       atomic.Int32
	waiting     atomic.Int32
}

func newSlowReporter(t *testing.T, fake *keyprovider.Fake, slowOwner string) *slowReporter {
	s := &slowReporter{Fake: fake, slow: slowOwner, released: make(chan struct{})}
	t.Cleanup(s.release)
	return s
}

// release lets every blocked read go. Safe to call more than once.
func (s *slowReporter) release() { s.once.Do(func() { close(s.released) }) }

func (s *slowReporter) block(ctx context.Context) error {
	s.waiting.Add(1)
	defer s.waiting.Add(-1)
	select {
	case <-s.released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *slowReporter) History(ctx context.Context, owner string, days int) (keyprovider.History, error) {
	s.calls.Add(1)
	if s.panics {
		panic("decode usage history")
	}
	if err := ctx.Err(); err != nil {
		return keyprovider.History{}, err
	}
	if owner == s.slow {
		if err := s.block(ctx); err != nil {
			return keyprovider.History{}, err
		}
	}
	return s.Fake.History(ctx, owner, days)
}

func (s *slowReporter) Windows(ctx context.Context, ref, owner string) ([]keyprovider.WindowUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.slowWindows {
		if err := s.block(ctx); err != nil {
			return nil, err
		}
	}
	return s.Fake.Windows(ctx, ref, owner)
}

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

// returnsWithin fails the test unless f returns within d. A read that waits
// behind another's lock, or for the gateway client's own timeout, hangs here
// instead of hanging the whole test run.
func returnsWithin[T any](t *testing.T, d time.Duration, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(d):
		t.Fatalf("did not return within %s", d)
		var zero T
		return zero
	}
}

// One person's slow read does not hold up another person's: nothing is
// shared between them, so neither waits behind the other (#173).
func TestUsageReaderReadsOneOwnerWhileAnotherReadHangs(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		"slow": {{Day: "2026-08-01", Tokens: 1}},
		"fast": {{Day: "2026-08-01", Tokens: 2}},
	}
	rep := newSlowReporter(t, fake, "slow")
	ur := newUsageReader(rep)
	ctx := context.Background()

	go ur.History(ctx, "slow")
	waitFor(t, func() bool { return rep.waiting.Load() == 1 })

	h := returnsWithin(t, time.Second, func() keyprovider.History {
		h, _ := ur.History(ctx, "fast")
		return h
	})
	if len(h.Days) != 1 || h.Days[0].Tokens != 2 {
		t.Errorf("fast owner's history = %+v, want theirs", h.Days)
	}
}

// The history and the windows are independent reads, so the page waits for
// the slower of them, not for both in turn.
func TestUserUsageReadsHistoryAndWindowsSideBySide(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"sub-1": {{Day: "2026-08-01", Tokens: 5}}}
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{"sk-live": {{Period: "1d", Used: 1, Limit: 2}}}
	rep := newSlowReporter(t, fake, "sub-1")
	rep.slowWindows = true
	u := &UI{usage: newUsageReader(rep)}

	done := make(chan usageReport, 1)
	go func() {
		done <- u.userUsage(context.Background(), &database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)
	}()
	waitFor(t, func() bool { return rep.waiting.Load() == 2 })
	rep.release()

	got := <-done
	if got.Incomplete {
		t.Error("report marked incomplete although both reads answered")
	}
	if got.Total != 5 || len(got.Windows) != 1 {
		t.Errorf("report has total %d and %d windows, want 5 and 1", got.Total, len(got.Windows))
	}
}

// The history is read on a goroutine of its own, where net/http does not
// recover a panic: one there, say from decoding an answer the gateway
// changed, would take the whole server down. It fails the report instead,
// as it failed the page load when the read ran on the request's goroutine.
func TestUserUsageSurvivesAPanicInTheHistoryRead(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.WindowsByRef = map[string][]keyprovider.WindowUsage{"sk-live": {{Period: "1d", Used: 1, Limit: 2}}}
	rep := newSlowReporter(t, fake, "")
	rep.panics = true
	u := &UI{usage: newUsageReader(rep)}

	got := u.userUsage(context.Background(), &database.APIKey{KeyRef: "sk-live"}, "sub-1", i18n.EN)
	if !got.Incomplete {
		t.Error("report not marked incomplete after the history read panicked")
	}
	if len(got.Windows) != 1 {
		t.Errorf("report has %d windows, want the one read beside the history", len(got.Windows))
	}
}
