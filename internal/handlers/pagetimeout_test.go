package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/keyprovider"
)

// With the gateway not answering, the dashboard comes back at its deadline
// and says the usage could not be loaded, instead of waiting out the gateway
// client's timeout and then the server's WriteTimeout (#173).
func TestDashboardAnswersWhileTheGatewayHangs(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "dash-hang")
	k := &database.APIKey{UserID: user.ID, KeyRef: "ref", KeyPrefix: "sk-hang", ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := store.ReplaceAPIKey(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	rep := newSlowReporter(t, fake, ui.cfg.LiteLLMUserID(user.OIDCSub))
	ui.usage = newUsageReader(rep)
	ui.upstreamTimeout = 50 * time.Millisecond

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := returnsWithin(t, time.Second, func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ui.Dashboard(rec, req)
		return rec
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "may be incomplete") {
		t.Error("the page does not say the usage could not be loaded")
	}
}

// The admin panel reads the model list with a deadline too: on a hung
// gateway it comes back with the last good list, or none, after a few
// seconds rather than the gateway client's timeout.
func TestPanelAnswersWhileTheModelListHangs(t *testing.T) {
	a, _, token, _ := newSyncTestAdmin(t, "panel-models-hang")
	lister := newSlowLister(t, keyprovider.NewFake())
	lister.slow = true
	a.models = newModelCache(lister)
	a.upstreamTimeout = 50 * time.Millisecond

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := returnsWithin(t, time.Second, func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.Panel(rec, req)
		return rec
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

// The gateway reads run side by side, each with the whole deadline: a model
// list that hangs does not leave the usage to be read once the deadline has
// passed, and the card does not call usage incomplete that the gateway
// answered.
func TestDashboardReadsUsageWhileTheModelListHangs(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "dash-models-hang")
	k := &database.APIKey{UserID: user.ID, KeyRef: "ref", KeyPrefix: "sk-hang", ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := store.ReplaceAPIKey(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	fake.UsageByOwner = map[string][]keyprovider.DailyUsage{
		ui.cfg.LiteLLMUserID(user.OIDCSub): {{Day: time.Now().UTC().Format("2006-01-02"), Tokens: 7}},
	}
	lister := newSlowLister(t, fake)
	lister.slow = true
	ui.models = newModelCache(lister)
	ui.usage = newUsageReader(newSlowReporter(t, fake, ""))
	ui.upstreamTimeout = 50 * time.Millisecond

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := returnsWithin(t, time.Second, func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ui.Dashboard(rec, req)
		return rec
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "may be incomplete") {
		t.Error("the usage is marked incomplete although only the model list hung")
	}
}
