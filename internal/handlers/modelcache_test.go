package handlers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// slowLister stands in for a gateway whose model list does not come: with
// slow set, ListModels blocks until the lister is released or the context
// ends. With panics set, it panics, as a bug in decoding the gateway's answer
// would. calls counts the ListModels reads, waiting the ones blocked now.
type slowLister struct {
	*keyprovider.Fake
	slow     bool
	panics   atomic.Bool
	released chan struct{}
	once     sync.Once
	calls    atomic.Int32
	waiting  atomic.Int32
}

func newSlowLister(t *testing.T, fake *keyprovider.Fake) *slowLister {
	l := &slowLister{Fake: fake, released: make(chan struct{})}
	t.Cleanup(l.release)
	return l
}

func (l *slowLister) release() { l.once.Do(func() { close(l.released) }) }

func (l *slowLister) ListModels(ctx context.Context) ([]string, error) {
	l.calls.Add(1)
	if l.panics.Load() {
		panic("decode model list")
	}
	if l.slow {
		l.waiting.Add(1)
		defer l.waiting.Add(-1)
		select {
		case <-l.released:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return l.Fake.ListModels(ctx)
}

// The embedding list, read on every dashboard load, must not wait behind a
// model list read that hangs: the two are read outside the shared lock.
func TestModelCacheServesEmbeddingsWhileAModelReadHangs(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.AvailableModels = []string{"a"}
	fake.EmbeddingByModel = map[string]bool{"e": true}
	l := newSlowLister(t, fake)
	l.slow = true
	c := newModelCache(l)
	ctx := context.Background()

	if emb := c.Embeddings(ctx); !emb["e"] {
		t.Fatalf("embeddings = %v, want them read", emb)
	}
	go c.Models(ctx)
	waitFor(t, func() bool { return l.waiting.Load() == 1 })

	emb := returnsWithin(t, time.Second, func() map[string]bool { return c.Embeddings(ctx) })
	if !emb["e"] {
		t.Errorf("embeddings = %v, want the cached list", emb)
	}
}

// During an outage the admin form keeps the last good list, and the gateway
// is tried once per failure TTL rather than once per page load.
func TestModelCacheRetriesAFailureOnlyAfterItsTTL(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.AvailableModels = []string{"a"}
	l := newSlowLister(t, fake)
	c := newModelCache(l)
	c.models.ttl = 0 // every load finds the list stale
	ctx := context.Background()

	if got := c.Models(ctx); len(got) != 1 {
		t.Fatalf("first read = %v, want the list", got)
	}
	fake.ModelsErr = errors.New("gateway down")
	for i := range 3 {
		if got := c.Models(ctx); len(got) != 1 || got[0] != "a" {
			t.Errorf("read %d = %v, want the last good list", i, got)
		}
	}
	if n := l.calls.Load(); n != 2 {
		t.Errorf("%d upstream reads, want 2: the good one, and the failed one that is remembered", n)
	}
}

// A refresh is not cancelled with its caller, so that the next load finds
// what it read, but it has a deadline of its own: a hung gateway does not
// hold it, and the list's next refresh, for the gateway client's timeout.
func TestModelCacheGivesARefreshItsOwnDeadline(t *testing.T) {
	l := newSlowLister(t, keyprovider.NewFake())
	l.slow = true
	c := newModelCache(l)
	c.models.timeout = 100 * time.Millisecond

	go c.Models(context.Background())
	waitFor(t, func() bool { return l.waiting.Load() == 1 })
	waitFor(t, func() bool { return l.waiting.Load() == 0 })
}

// A panic in a refresh fails that refresh and keeps the last good list.
// singleflight raises it again on a goroutine nothing can recover, so
// unhandled it would take the whole server down rather than one page load.
func TestModelCacheSurvivesAPanicInARefresh(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.AvailableModels = []string{"a"}
	l := newSlowLister(t, fake)
	c := newModelCache(l)
	c.models.ttl = 0 // every load finds the list stale
	ctx := context.Background()

	if got := c.Models(ctx); len(got) != 1 {
		t.Fatalf("first read = %v, want the list", got)
	}
	l.panics.Store(true)
	if got := c.Models(ctx); len(got) != 1 || got[0] != "a" {
		t.Errorf("after a panicking refresh = %v, want the last good list", got)
	}
}
