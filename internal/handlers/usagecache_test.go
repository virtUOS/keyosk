package handlers

import (
	"context"
	"errors"
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
// once. calls counts the History reads; waiting is how many reads are
// blocked right now, so a test can tell that a read is under way.
type slowReporter struct {
	*keyprovider.Fake
	slow        string
	slowWindows bool
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
	if owner == s.slow {
		if err := s.block(ctx); err != nil {
			return keyprovider.History{}, err
		}
	}
	return s.Fake.History(ctx, owner, days)
}

func (s *slowReporter) Windows(ctx context.Context, ref, owner string) ([]keyprovider.WindowUsage, error) {
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

// One person's slow upstream read must not hold up another person whose
// history is cached: the cache's lock is shared, so the read must not run
// under it (#173).
func TestUsageCacheServesACachedOwnerWhileAnotherReadHangs(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		"slow": {{Day: "2026-08-01", Tokens: 1}},
		"fast": {{Day: "2026-08-01", Tokens: 2}},
	}
	rep := newSlowReporter(t, fake, "slow")
	c := newUsageCache(rep)
	ctx := context.Background()

	if _, err := c.History(ctx, "fast"); err != nil {
		t.Fatal(err)
	}
	go c.History(ctx, "slow")
	waitFor(t, func() bool { return rep.waiting.Load() == 1 })

	h := returnsWithin(t, time.Second, func() keyprovider.History {
		h, _ := c.History(ctx, "fast")
		return h
	})
	if len(h.Days) != 1 || h.Days[0].Tokens != 2 {
		t.Errorf("fast owner's history = %+v, want the cached one", h.Days)
	}
}

// Concurrent loads for the same person share one upstream read.
func TestUsageCacheSharesOneReadPerOwner(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"slow": {{Day: "2026-08-01", Tokens: 1}}}
	rep := newSlowReporter(t, fake, "slow")
	c := newUsageCache(rep)

	var wg sync.WaitGroup
	got := make([]keyprovider.History, 4)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = c.History(context.Background(), "slow")
		}()
	}
	waitFor(t, func() bool { return rep.waiting.Load() == 1 })
	// Give the other loads time to arrive while the first read hangs. One
	// that arrives after the result is cached makes no read either.
	time.Sleep(20 * time.Millisecond)
	rep.release()
	wg.Wait()

	if n := rep.calls.Load(); n != 1 {
		t.Errorf("%d upstream reads for one owner, want 1", n)
	}
	for i, h := range got {
		if len(h.Days) != 1 {
			t.Errorf("load %d got %+v, want the shared result", i, h.Days)
		}
	}
}

// During an outage every page load would otherwise try the gateway again. A
// failed read is remembered for a short while, and in that time the cache
// returns the error, never an older figure in its place.
func TestUsageCacheRemembersAFailureBriefly(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"sub-1": {{Day: "2026-08-01", Tokens: 5}}}
	rep := newSlowReporter(t, fake, "")
	c := newUsageCache(rep)
	c.ttl = 0 // every load finds its entry stale
	ctx := context.Background()

	if h, err := c.History(ctx, "sub-1"); err != nil || len(h.Days) != 1 {
		t.Fatalf("first read = %+v, %v", h.Days, err)
	}
	fake.UsageErr = errors.New("gateway down")
	for i := range 3 {
		h, err := c.History(ctx, "sub-1")
		if err == nil {
			t.Fatalf("read %d: no error while the gateway is down", i)
		}
		if len(h.Days) != 0 {
			t.Errorf("read %d: served %+v in place of the error", i, h.Days)
		}
	}
	if n := rep.calls.Load(); n != 2 {
		t.Errorf("%d upstream reads, want 2: the good one, and the failed one that is remembered", n)
	}
}

// A load whose deadline passes gets its error then, not when the gateway
// finally answers. The read goes on, so the next load finds the result
// cached rather than reading again.
func TestUsageCacheReturnsAtTheCallersDeadline(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{"slow": {{Day: "2026-08-01", Tokens: 1}}}
	rep := newSlowReporter(t, fake, "slow")
	c := newUsageCache(rep)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := returnsWithin(t, time.Second, func() error {
		_, err := c.History(ctx, "slow")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}

	rep.release()
	h, err := c.History(context.Background(), "slow")
	if err != nil || len(h.Days) != 1 {
		t.Fatalf("after the gateway answered: %+v, %v", h.Days, err)
	}
	if n := rep.calls.Load(); n != 1 {
		t.Errorf("%d upstream reads, want 1: the one the first load gave up on", n)
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
	u := &UI{usage: newUsageCache(rep)}

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
