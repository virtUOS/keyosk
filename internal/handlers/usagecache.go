package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// usageWindowDays is how far back the dashboard reports unless configured
// otherwise (USAGE_HISTORY_DAYS). Long enough to cover a month's key
// validity.
const usageWindowDays = 30

// usageCacheTTL bounds how stale a usage report can be. LiteLLM takes about
// fifteen seconds to propagate spend anyway, so a fresh read per page load
// would be both wasteful and no more accurate.
const usageCacheTTL = 60 * time.Second

// usageFailureTTL is how long a failed refresh is remembered. While the
// gateway is down, every page load would otherwise read upstream again and
// wait out the client's timeout; this makes an outage cost one attempt per
// person in that time.
const usageFailureTTL = 10 * time.Second

// usageCache serves usage without calling upstream on every page load.
//
// The history is keyed by the person it belongs to, as it is upstream, so a
// rotation keeps serving it.
//
// Every accessor holds the same invariant: a failed refresh returns the error
// and no figure rather than a stale one, and leaves no figure behind for a
// later read to pick up. The dashboard says so instead of showing a silently
// shorter report.
//
// The lock covers only the entries. A refresh runs outside it, one per person
// at a time, so a slow gateway holds up the people whose history is being
// read and nobody else.
type usageCache struct {
	reporter keyprovider.UsageReporter
	// windowDays is how far back History reaches.
	windowDays int
	// ttl bounds how stale a history can be; failureTTL is how long a failed
	// refresh is remembered.
	ttl, failureTTL time.Duration

	mu      sync.Mutex
	entries map[string]usageEntry
	// refreshes shares one upstream read among the loads that need the same
	// person's history at the same time.
	refreshes singleflight.Group
}

// usageEntry is one person's history, or the error their last refresh ended
// in, with when it was fetched.
type usageEntry struct {
	history   keyprovider.History
	err       error
	fetchedAt time.Time
}

func newUsageCache(r keyprovider.UsageReporter) *usageCache {
	return &usageCache{
		reporter:   r,
		windowDays: usageWindowDays,
		ttl:        usageCacheTTL,
		failureTTL: usageFailureTTL,
		entries:    make(map[string]usageEntry),
	}
}

// fresh reports whether e can still be served: a history for ttl, a failure
// for failureTTL.
func (c *usageCache) fresh(e usageEntry) bool {
	ttl := c.ttl
	if e.err != nil {
		ttl = c.failureTTL
	}
	return time.Since(e.fetchedAt) < ttl
}

// History returns the cached per-day and per-model usage of a person,
// refreshing when stale.
//
// Both halves come from one upstream read, so they are cached as one entry and
// can never disagree about the window they cover. A failed refresh returns an
// empty History with the error, and is remembered for failureTTL.
//
// The refresh is not cancelled with ctx: a load that gives up at its deadline,
// or a client that went away, leaves it to finish, so the next load finds the
// result cached instead of reading again.
func (c *usageCache) History(ctx context.Context, ownerID string) (keyprovider.History, error) {
	if c.reporter == nil || ownerID == "" {
		return keyprovider.History{}, nil
	}

	c.mu.Lock()
	e, ok := c.entries[ownerID]
	c.mu.Unlock()
	if ok && c.fresh(e) {
		return e.history, e.err
	}

	ch := c.refreshes.DoChan(ownerID, func() (any, error) {
		h, err := c.reporter.History(context.WithoutCancel(ctx), ownerID, c.windowDays)
		if err != nil {
			slog.Error("read usage history", "err", err)
			h = keyprovider.History{}
		}
		c.mu.Lock()
		c.entries[ownerID] = usageEntry{history: h, err: err, fetchedAt: time.Now()}
		c.mu.Unlock()
		return h, err
	})
	select {
	case r := <-ch:
		return r.Val.(keyprovider.History), r.Err
	case <-ctx.Done():
		return keyprovider.History{}, ctx.Err()
	}
}

// Windows reports consumption against every allowance window of the key and
// its owner.
//
// Uncached: it is the figure users act on when they are close to the limit,
// and a minute-stale number there is worse than a fresh call.
func (c *usageCache) Windows(ctx context.Context, ref, ownerID string) ([]keyprovider.WindowUsage, error) {
	if c.reporter == nil || ref == "" {
		return nil, nil
	}
	return c.reporter.Windows(ctx, ref, ownerID)
}
