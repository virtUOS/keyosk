package oidc

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/virtuos/keyosk/internal/oidc/oidctest"
)

// loginRedirect runs /login on the frontend host and returns where it sent
// the browser, the authorization request, and the state cookie it set.
func loginRedirect(t *testing.T, p *Provider) (authURL string, cookie *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, httptest.NewRequest(http.MethodGet, "http://localhost:8080/login", nil))
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusFound || len(cookies) != 1 {
		t.Fatalf("login answered %d with %d cookies, want a redirect and the state cookie", rec.Code, len(cookies))
	}
	return rec.Header().Get("Location"), cookies[0]
}

func queryOf(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// The login carries a PKCE challenge, so the code the provider issues is
// bound to this login: a code that leaks, or that an attacker injects into
// someone else's callback, cannot be redeemed without the verifier (#165).
func TestLoginSendsAPKCEChallenge(t *testing.T) {
	p := newTestProvider(t, oidctest.NewServer(t))
	authURL, _ := loginRedirect(t, p)
	q := queryOf(t, authURL)
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if c := q.Get("code_challenge"); len(c) != 43 {
		t.Errorf("code_challenge = %q, want 43 characters", c)
	}
}

// The exchange answers the challenge with the verifier the login kept.
func TestCallbackExchangesWithTheLoginsVerifier(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)

	authURL, cookie := loginRedirect(t, p)
	code, state := m.Authorize(t, authURL, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"})
	res, _, err := callback(p, state, code, cookie)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if res.UserInfo.Subject != "sub-1" {
		t.Errorf("subject = %q, want sub-1", res.UserInfo.Subject)
	}
}

// A code issued for another login's challenge fails at the exchange: this
// login's verifier does not answer it.
func TestCallbackRefusesAnotherLoginsCode(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)

	mineURL, mine := loginRedirect(t, p)
	theirsURL, _ := loginRedirect(t, p)
	code, _ := m.Authorize(t, theirsURL, oidctest.Claims{Sub: "attacker", Email: "x@uni-osnabrueck.de"})
	if _, _, err := callback(p, queryOf(t, mineURL).Get("state"), code, mine); err == nil {
		t.Fatal("a code issued for another login's challenge was redeemed")
	}
}

// An empty state names the cookie "oidc_state_", whose empty value would
// match it. Nothing legitimate sends one, so it is refused before any cookie
// is looked at.
func TestCallbackRejectsAnEmptyState(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	// A code the exchange would accept, so the refusal is the state's.
	m.IssueCode("code-1", oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"})

	if _, _, err := callback(p, "", "code-1", &http.Cookie{Name: "oidc_state_", Value: ""}); err == nil {
		t.Fatal("a callback with an empty state was accepted")
	}
}
