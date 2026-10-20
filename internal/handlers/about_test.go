package handlers

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/buildinfo"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
)

// renderAbout renders about.html for build in lang, as the handler would.
func renderAbout(t *testing.T, lang i18n.Lang, build buildinfo.Info) string {
	t.Helper()
	var buf bytes.Buffer
	if err := parseAboutTemplate().Execute(&buf, newAboutData(build, aboutData{
		Lang: lang, Path: "/about", TitleKey: "about.title", CSRFToken: "TOK",
		User: &database.User{Name: "lkiesow", Email: "l@uni-osnabrueck.de"},
	})); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String()
}

var testBuild = buildinfo.Info{
	Version:   "v0.8.2-0.20261020120000-0123456789ab+dirty",
	Commit:    "0123456789abcdef0123456789abcdef01234567",
	Date:      time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	Modified:  true,
	GoVersion: "go1.27.0",
}

// The page says which build runs, links its commit, and leads to the
// documentation, the issue tracker and the project page.
func TestAboutPageShowsTheBuildAndLinks(t *testing.T) {
	page := renderAbout(t, i18n.EN, testBuild)
	// html/template writes the + of +dirty as &#43;, which reads the same.
	text := html.UnescapeString(page)
	for _, want := range []string{
		testBuild.Version,
		`href="https://github.com/virtUOS/keyosk/commit/` + testBuild.Commit + `"`,
		">0123456789ab</a> (with local changes)",
		"2026-10-20 12:00 UTC",
		"go1.27.0",
		`href="https://virtuos.github.io/keyosk/"`,
		`href="https://github.com/virtUOS/keyosk/issues"`,
		`href="https://github.com/virtUOS/keyosk"`,
		`class="brand-mark"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("about page does not contain %q", want)
		}
	}
	// The mark in the page must not share its mask's id with the header's.
	if n := strings.Count(page, `id="brand-key"`); n != 1 {
		t.Errorf("mask id brand-key appears %d times, want once", n)
	}
	if !strings.Contains(page, `id="brand-key-about"`) || !strings.Contains(page, `url(#brand-key-about)`) {
		t.Error("the page's own mark has no mask of its own")
	}
	// The menu does not link the page it is on.
	if strings.Contains(accountMenu(page), `href="/about"`) {
		t.Error("menu links the about page from the about page")
	}
}

// A build without .git knows only its version and toolchain. The rows for
// what it does not know are left out rather than shown empty or linking
// nowhere.
func TestAboutPageLeavesOutWhatTheBuildDidNotRecord(t *testing.T) {
	page := renderAbout(t, i18n.EN, buildinfo.Info{Version: "(devel)", GoVersion: "go1.27.0"})
	if !strings.Contains(page, "(devel)") {
		t.Error("version missing")
	}
	for _, unwanted := range []string{"Commit", "/commit/", "Commit date"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("about page shows %q for a build without VCS data", unwanted)
		}
	}
}

func TestAboutPageIsTranslated(t *testing.T) {
	page := renderAbout(t, i18n.DE, testBuild)
	text := strings.ToLower(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(page, " "))
	for _, m := range []string{"the", "and", "with", "your", "issue"} {
		if regexp.MustCompile(`\b` + m + `\b`).MatchString(text) {
			t.Errorf("German about page contains the English word %q", m)
		}
	}
	if m := regexp.MustCompile(`\b(about|nav)\.[a-z][a-z.]*[a-z]\b`).FindAllString(text, -1); len(m) > 0 {
		t.Errorf("about page renders raw catalogue keys: %v", m)
	}
}

func TestAboutPageNeedsALogin(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "about1")
	rec := httptest.NewRecorder()
	ui.About(rec, httptest.NewRequest(http.MethodGet, "/about", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous visitor got %d to %q, want a redirect to /login",
			rec.Code, rec.Header().Get("Location"))
	}
}

func TestAboutPageRendersForASignedInUser(t *testing.T) {
	ui, _, _, _ := newTestUI(t, "about2")
	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})
	rec := httptest.NewRecorder()
	ui.About(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `href="https://github.com/virtUOS/keyosk/issues"`) {
		t.Error("about page has no link to the issue tracker")
	}
}

// Every page with the account menu leads to the about page.
func TestAccountMenuLinksAbout(t *testing.T) {
	var buf bytes.Buffer
	if err := parseDashboardTemplate().Execute(&buf, dashboardData{
		Lang: i18n.EN, Path: "/", CSRFToken: "TOK",
		User: &database.User{Name: "lkiesow", Email: "l@uni-osnabrueck.de"},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(accountMenu(buf.String()), `href="/about"`) {
		t.Error("menu does not lead to the about page")
	}
}
