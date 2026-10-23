package config

import (
	"strings"
	"testing"
)

// An operator who still sets a setting an older version read expects behaviour
// Keyosk no longer has. Startup fails and names every one that is set, with
// what replaced it, so they can all be removed at once. An emptied line in an
// env file counts as unset.
func TestLoadRefusesObsoleteSettings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_IDS", "sub-1")
	t.Setenv("ADMIN_EMAILS", " ")
	t.Setenv("COOKIE_SECURE", "true")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	for _, want := range []string{
		"ADMIN_IDS (" + adminRecordedOnAccount + ")",
		"COOKIE_SECURE (cookies are secure when FRONTEND_URL is https)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "ADMIN_EMAILS") {
		t.Errorf("error %q names ADMIN_EMAILS, which is blank", err)
	}
}
