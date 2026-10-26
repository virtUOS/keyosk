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
	ui.usage = newUsageCache(rep)
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
