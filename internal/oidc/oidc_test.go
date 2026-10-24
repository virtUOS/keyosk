package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/oidc/oidctest"
)

func newTestProvider(t *testing.T, m *oidctest.Server) *Provider {
	t.Helper()
	cfg := &config.Config{
		OIDCIssuerURL:    m.Issuer(),
		OIDCClientID:     "keyosk",
		OIDCClientSecret: "secret",
		FrontendURL:      "http://localhost:8080",
	}
	p, err := NewProvider(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func TestDiscoveryFindsEndSessionEndpoint(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	url := p.LogoutURL("some-id-token")
	if !strings.Contains(url, "/protocol/openid-connect/logout") {
		t.Fatalf("LogoutURL = %q, want the discovered end_session_endpoint", url)
	}
	if !strings.Contains(url, "id_token_hint=some-id-token") {
		t.Errorf("LogoutURL missing id_token_hint: %q", url)
	}
}

func TestValidateLogoutTokenAcceptsValid(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	tok := m.SignLogoutToken(t, oidctest.NewLogoutToken("user-sub-123", "keyosk"))

	sub, err := p.ValidateLogoutToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("valid logout token rejected: %v", err)
	}
	if sub != "user-sub-123" {
		t.Errorf("sub = %q, want user-sub-123", sub)
	}
}

// The spec forbids a nonce in a logout token; accepting one would let an
// ordinary id_token be replayed as a logout instruction.
func TestValidateLogoutTokenRejectsNonce(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	tok := oidctest.NewLogoutToken("user-sub-123", "keyosk")
	tok.Claims = map[string]any{"nonce": "must-not-be-here"}

	if _, err := p.ValidateLogoutToken(context.Background(), m.SignLogoutToken(t, tok)); err == nil {
		t.Fatal("logout token with nonce was accepted")
	}
}

// A plain id_token has no backchannel-logout event and must not log anyone out.
func TestValidateLogoutTokenRejectsPlainIDToken(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	idToken, err := m.SignIDToken(
		oidctest.Claims{Sub: "user-sub-123", Email: "a@uni-osnabrueck.de"},
		"keyosk", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ValidateLogoutToken(context.Background(), idToken); err == nil {
		t.Fatal("plain id_token accepted as a logout token")
	}
}

// Tokens minted for a different client must not be honoured.
func TestValidateLogoutTokenRejectsWrongAudience(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	tok := m.SignLogoutToken(t, oidctest.NewLogoutToken("user-sub-123", "some-other-client"))

	if _, err := p.ValidateLogoutToken(context.Background(), tok); err == nil {
		t.Fatal("logout token for another audience was accepted")
	}
}

// A logout token is good only for a few minutes, so one captured on the way,
// from a proxy log say, cannot log its user out again later. Tokens without
// exp are accepted, since older versions of the spec did not require it, and
// so is a little skew between the clocks. Without the logout event a token is
// no logout token, and one with only sid names no user to log out.
func TestValidateLogoutTokenChecksItsClaims(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	now := time.Now()

	for _, tc := range []struct {
		name   string
		claims map[string]any
		ok     bool
	}{
		{"fresh", nil, true},
		{"without exp", map[string]any{"exp": nil}, true},
		{"from a clock slightly ahead", map[string]any{"iat": now.Add(30 * time.Second).Unix()}, true},
		{"without iat", map[string]any{"iat": nil}, false},
		{"issued long ago", map[string]any{"iat": now.Add(-time.Hour).Unix(), "exp": nil}, false},
		{"issued in the future", map[string]any{"iat": now.Add(time.Hour).Unix()}, false},
		{"expired", map[string]any{"iat": now.Add(-4 * time.Minute).Unix(), "exp": now.Add(-2 * time.Minute).Unix()}, false},
		{"without the logout event", map[string]any{"events": nil}, false},
		{"with only sid", map[string]any{"sub": nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok := oidctest.NewLogoutToken("user-sub-123", "keyosk")
			tok.Claims = tc.claims
			_, err := p.ValidateLogoutToken(context.Background(), m.SignLogoutToken(t, tok))
			if tc.ok && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("accepted")
			}
		})
	}
}

// When the header names a type, it must be a logout token's or the generic
// JWT, which many IdPs put on all their tokens; any other type the IdP signs
// for this client is not a logout token.
func TestValidateLogoutTokenChecksTheType(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)

	for typ, ok := range map[string]bool{
		"logout+jwt":             true,
		"application/logout+jwt": true,
		"Logout+JWT":             true,
		"":                       true,
		"JWT":                    true,
		"at+jwt":                 false,
	} {
		tok := oidctest.NewLogoutToken("user-sub-123", "keyosk")
		tok.Typ = typ
		_, err := p.ValidateLogoutToken(context.Background(), m.SignLogoutToken(t, tok))
		if ok && err != nil {
			t.Errorf("typ %q refused: %v", typ, err)
		}
		if !ok && err == nil {
			t.Errorf("typ %q accepted", typ)
		}
	}
}

func TestLoginMovesToFrontendURLHost(t *testing.T) {
	p := newTestProvider(t, oidctest.NewServer(t))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/login?next=x", nil)
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://localhost:8080/login?") ||
		!strings.Contains(loc, "canonical=1") || !strings.Contains(loc, "next=x") {
		t.Errorf("Location = %q, want /login on localhost:8080 with the query kept", loc)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("state cookie set on the wrong host; the callback would not see it")
	}
}

func TestLoginOnFrontendURLHostStartsAuth(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/login", nil)
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, req)

	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, m.Issuer()) {
		t.Errorf("Location = %q, want the provider's authorize endpoint", loc)
	}
	if len(rec.Result().Cookies()) != 1 {
		t.Errorf("cookies = %d, want the state cookie", len(rec.Result().Cookies()))
	}
}

// A proxy that rewrites Host would never match; after one hop the login must
// proceed rather than redirect forever.
func TestLoginDoesNotLoopWhenHostIsRewritten(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	req := httptest.NewRequest(http.MethodGet, "http://upstream:8080/login?canonical=1", nil)
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, req)

	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, m.Issuer()) {
		t.Errorf("Location = %q, want the provider's authorize endpoint", loc)
	}
}

// Extra scopes are requested on top of the standard ones, for IdPs that send
// groups only when asked.
func TestLoginRequestsExtraScopes(t *testing.T) {
	m := oidctest.NewServer(t)
	cfg := *newTestProvider(t, m).cfg
	cfg.OIDCExtraScopes = []string{"groups"}
	p, err := NewProvider(context.Background(), &cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, httptest.NewRequest(http.MethodGet, "http://localhost:8080/login", nil))
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loc.Query().Get("scope"); got != "openid profile email groups" {
		t.Errorf("scope = %q, want openid profile email groups", got)
	}
}
