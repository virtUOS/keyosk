package config

import "testing"

// The cookies are secure exactly when people reach the portal over HTTPS,
// with no setting to forget.
func TestCookieSecureFollowsFrontendURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://keyosk.example.org":  true,
		"HTTPS://keyosk.example.org/": true,
		"http://localhost:8080":       false,
	} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if cfg.CookieSecure != want {
			t.Errorf("%s: CookieSecure = %v, want %v", url, cfg.CookieSecure, want)
		}
	}
}

// Without a scheme the portal could not tell whether it is served over
// HTTPS, and the links in expiry emails would not work either.
func TestFrontendURLMustBeAbsolute(t *testing.T) {
	for _, url := range []string{"keyosk.example.org", "/keyosk", "ftp://keyosk.example.org", "https://"} {
		setRequiredEnv(t)
		t.Setenv("FRONTEND_URL", url)
		if _, err := Load(); err == nil {
			t.Errorf("%s: Load succeeded, want an error", url)
		}
	}
}

// COOKIE_SECURE is no longer read, whatever it says; startup warns about it.
func TestCookieSecureSettingIsIgnored(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("FRONTEND_URL", "https://keyosk.example.org")
	t.Setenv("COOKIE_SECURE", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure || !cfg.CookieSecureSet {
		t.Errorf("CookieSecure = %v, CookieSecureSet = %v, want both true", cfg.CookieSecure, cfg.CookieSecureSet)
	}
}
