package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// modelCacheTTL bounds how stale the admin form's model list can be. Models
// change rarely, and a stale entry only affects which checkboxes are offered.
const modelCacheTTL = 5 * time.Minute

// modelFailureTTL is how long a failed refresh holds off the next one, so an
// outage costs one attempt per list in that time rather than one per page
// load, each waiting out the client's timeout.
const modelFailureTTL = 10 * time.Second

// cached is one list the cache holds: the last good value, when it was
// fetched, and when a refresh last failed. The lock covers the fields; a
// refresh runs outside it, one at a time.
type cached[T any] struct {
	ttl time.Duration

	mu        sync.Mutex
	value     T
	fetchedAt time.Time
	failedAt  time.Time
	refresh   singleflight.Group
}

// get returns the value, refreshing it through fetch when it is stale. A
// failed refresh keeps the last good value and is not tried again for
// modelFailureTTL. A caller whose ctx ends first gets the last good value
// too; the refresh goes on, and the next call finds its result.
func (c *cached[T]) get(ctx context.Context, what string, fetch func(context.Context) (T, error)) T {
	c.mu.Lock()
	v := c.value
	stale := time.Since(c.fetchedAt) >= c.ttl && time.Since(c.failedAt) >= modelFailureTTL
	c.mu.Unlock()
	if !stale {
		return v
	}

	ch := c.refresh.DoChan("", func() (any, error) {
		v, err := fetch(context.WithoutCancel(ctx))
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			slog.Error("list "+what, "err", err)
			c.failedAt = time.Now()
			return c.value, nil
		}
		c.value, c.fetchedAt = v, time.Now()
		return v, nil
	})
	select {
	case r := <-ch:
		return r.Val.(T)
	case <-ctx.Done():
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.value
	}
}

// modelCache serves the gateway's model lists to the admin form and the
// dashboard without calling upstream on every page load.
//
// A failed refresh keeps serving the last good list: an admin editing an
// unrelated field should not lose the model checkboxes because the gateway
// blipped.
type modelCache struct {
	lister   keyprovider.ModelLister
	embedder keyprovider.EmbeddingLister

	models     cached[[]string]
	embeddings cached[map[string]bool]
}

func newModelCache(l keyprovider.ModelLister) *modelCache {
	e, _ := l.(keyprovider.EmbeddingLister)
	return &modelCache{lister: l, embedder: e,
		models:     cached[[]string]{ttl: modelCacheTTL},
		embeddings: cached[map[string]bool]{ttl: modelCacheTTL}}
}

// Embeddings reports which models the gateway serves as embedding models, so
// the dashboard can show the right example request for each.
//
// A failed lookup returns nil rather than an error: every model then gets the
// chat example, which is what the page did before this existed. A wrong example
// for one model is a smaller harm than no model list at all.
func (c *modelCache) Embeddings(ctx context.Context) map[string]bool {
	if c.embedder == nil {
		return nil
	}
	return c.embeddings.get(ctx, "embedding models", c.embedder.EmbeddingModels)
}

// Models returns the cached list, refreshing it when stale.
func (c *modelCache) Models(ctx context.Context) []string {
	if c.lister == nil {
		return nil
	}
	return c.models.get(ctx, "models", c.lister.ListModels)
}
