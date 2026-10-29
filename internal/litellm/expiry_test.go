package litellm

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// The gateway gets the expiry to the second, so it stops honouring the key at
// the moment the dashboard shows, and not up to a day later, as a whole
// number of days from now rounded up did (#187).
func TestKeyExpiryIsSentToTheSecond(t *testing.T) {
	g := newGatewayStub(t)
	p := NewProvider(NewClient(g.srv.URL, "mk"))
	ctx := context.Background()
	expires := time.Now().Add(36*time.Hour + 30*time.Minute)

	res, err := p.CreateKey(ctx, keyprovider.KeyRequest{Alias: "a", User: keyprovider.User{ID: "sub"}, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateExpiry(ctx, res.Ref, expires.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if len(g.keyBodies) != 2 {
		t.Fatalf("made %d key calls, want the creation and the update", len(g.keyBodies))
	}
	for i, want := range []time.Duration{36*time.Hour + 30*time.Minute, 60*time.Hour + 30*time.Minute} {
		got := durationSeconds(t, g.keyBodies[i]["duration"])
		if d := got - want; d < -2*time.Second || d > 2*time.Second {
			t.Errorf("call %d sent duration %v, want about %s", i, g.keyBodies[i]["duration"], want)
		}
	}
}

func durationSeconds(t *testing.T, v any) time.Duration {
	t.Helper()
	s, ok := v.(string)
	if !ok || !strings.HasSuffix(s, "s") {
		t.Fatalf("duration = %v, want a number of seconds", v)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "s"))
	if err != nil {
		t.Fatalf("duration = %q: %v", s, err)
	}
	return time.Duration(n) * time.Second
}
