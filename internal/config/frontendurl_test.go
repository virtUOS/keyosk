package config

import "testing"

// The cookies are secure exactly when people reach the portal over HTTPS,
// with no setting to forget. FRONTEND_URL tells: it holds the OIDC callback,
// so with the wrong scheme nobody could log in.
func TestCookieSecureFollowsFrontendURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://keyosk.example.org":  true,
		"HTTPS://keyosk.example.org":  true,
		"https://keyosk.example.org/": true,
		"http://localhost:8080":       false,
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", url, err)
		}
		if cfg.CookieSecure != want {
			t.Errorf("%q: CookieSecure = %v, want %v", url, cfg.CookieSecure, want)
		}
	}
}

// The routes, the links in the pages and the __Host- cookies sit at the root
// of the host, so a FRONTEND_URL the portal cannot be served at is refused
// rather than left to fail at the first login.
func TestFrontendURLMustBeTheRootOfAHost(t *testing.T) {
	for _, url := range []string{
		"keyosk.example.org",
		"ftp://keyosk.example.org",
		"https://",
		"https://keyosk.example.org\r",
		"https://example.org/keyosk",
		"https://keyosk.example.org/?x=1",
		"https://keyosk.example.org/?",
		"https://keyosk.example.org/#top",
		"https://keyosk.example.org//",
		"https://user:pw@keyosk.example.org",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		if _, err := Load(); err == nil {
			t.Errorf("%q: Load succeeded, want an error", url)
		}
	}
}

// FRONTEND_URL is kept as scheme://host, whatever spelling the operator
// chose, so the OIDC callback, the redirect after logout and the links in
// emails all agree, and the identity provider, which compares redirect URIs
// byte for byte, sees a predictable one.
func TestFrontendURLIsNormalized(t *testing.T) {
	for frontend, want := range map[string]string{
		"https://keyosk.example.org":  "https://keyosk.example.org",
		"https://keyosk.example.org/": "https://keyosk.example.org",
		"HTTPS://keyosk.example.org/": "https://keyosk.example.org",
		"http://localhost:8080":       "http://localhost:8080",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", frontend)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", frontend, err)
		}
		if cfg.FrontendURL != want {
			t.Errorf("%q: FrontendURL = %q, want %q", frontend, cfg.FrontendURL, want)
		}
	}
}

// The OIDC callback is /callback under FRONTEND_URL, with or without its
// trailing slash.
func TestOIDCRedirectURLFollowsFrontendURL(t *testing.T) {
	for frontend, want := range map[string]string{
		"https://keyosk.example.org":  "https://keyosk.example.org/callback",
		"https://keyosk.example.org/": "https://keyosk.example.org/callback",
		"HTTPS://keyosk.example.org":  "https://keyosk.example.org/callback",
		"http://localhost:8080":       "http://localhost:8080/callback",
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", frontend)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", frontend, err)
		}
		if got := cfg.OIDCRedirectURL(); got != want {
			t.Errorf("%q: OIDCRedirectURL() = %q, want %q", frontend, got, want)
		}
	}
}
