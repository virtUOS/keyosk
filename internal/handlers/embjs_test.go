package handlers

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/web"
)

// staticJS returns a script from web/static, where the pages' scripts live:
// the CSP allows no inline script.
func staticJS(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(web.StaticFS, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var pageDataBlock = regexp.MustCompile(`(?s)<script type="application/json" id="page-data">(.*?)</script>`)

// pageData parses the #page-data block a page hands its script. It must be
// JSON: the script reads it with JSON.parse, which fails on anything that is
// merely valid JavaScript.
func pageData(t *testing.T, page string) map[string]any {
	t.Helper()
	m := pageDataBlock.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("page has no #page-data block")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(m[1]), &data); err != nil {
		t.Fatalf("#page-data is not JSON: %v\n%s", err, m[1])
	}
	return data
}

// The map reaches the script through the page data. A nil map arrives as
// null, and the script must still get something a property lookup works on:
// `null[model]` throws and would break every curl example on the page, not
// just an embedding one.
func TestEmbeddingMapRendersAsUsableJS(t *testing.T) {
	render := func(m map[string]bool) any {
		d := dashboardData{
			Lang: i18n.EN, User: &database.User{Name: "T", Email: "t@e.com"},
			APIKey:     &database.APIKey{KeyPrefix: "sk", ExpiresAt: time.Now().Add(time.Hour)},
			APIBaseURL: "https://gw/v1", CSRFToken: "T",
			Models: []string{"m"}, EmbeddingModels: m,
		}
		var b bytes.Buffer
		if err := parseDashboardTemplate().Execute(&b, d); err != nil {
			t.Fatal(err)
		}
		return pageData(t, b.String())["embeddingModels"]
	}

	if got := render(nil); got != nil {
		t.Errorf("nil map rendered as %v, want null", got)
	}
	if !strings.Contains(staticJS(t, "dashboard.js"), "DATA.embeddingModels || {}") {
		t.Error("the script does not guard against a null map; a lookup on null throws")
	}
	got, _ := render(map[string]bool{"bge-m3": true}).(map[string]any)
	if got["bge-m3"] != true {
		t.Errorf("populated map rendered as %v", got)
	}
}

// What the server puts in the page data comes back unchanged, however it is
// spelled: html/template escapes it for the script element, and JSON.parse
// must undo exactly that. A "</script>" must not end the block early.
func TestPageDataSurvivesAwkwardText(t *testing.T) {
	const base = `https://gw.example/v1?a=1&b="2"</script><b>`
	var b bytes.Buffer
	if err := parseDashboardTemplate().Execute(&b, dashboardData{
		Lang: i18n.DE, User: &database.User{Name: "T", Email: "t@e.com"},
		APIBaseURL: base, CSRFToken: "T",
	}); err != nil {
		t.Fatal(err)
	}
	data := pageData(t, b.String())
	if data["baseURL"] != base {
		t.Errorf("baseURL = %q, want %q", data["baseURL"], base)
	}
	if data["copied"] != i18n.T(i18n.DE, "dash.copied") {
		t.Errorf("copied = %q, want the German word", data["copied"])
	}
	if strings.Count(b.String(), "</script><b>") != 0 {
		t.Error("the base URL closes the script element")
	}

	b.Reset()
	if err := parseAdminTemplate().Execute(&b, adminData{
		Lang:      i18n.DE,
		Profiles:  []database.Profile{},
		Users:     []userRow{},
		CSRFToken: "T",
	}); err != nil {
		t.Fatal(err)
	}
	data = pageData(t, b.String())
	periods, _ := data["periods"].([]any)
	if len(periods) != 4 {
		t.Fatalf("periods = %v, want four", data["periods"])
	}
	if p, _ := periods[1].([]any); len(p) != 3 || p[0] != "24h" || p[1] != i18n.T(i18n.DE, "period.daily") || p[2] != 24.0 {
		t.Errorf("daily period = %v", periods[1])
	}
	if profile, _ := data["profile"].(map[string]any); profile["update"] != i18n.T(i18n.DE, "admin.form.update") {
		t.Errorf("profile texts = %v", data["profile"])
	}
}

// The CSP allows no inline script, so a <script> with a body or an onclick
// in a template is code that silently does nothing. Only the JSON data block
// may have a body; the browser does not run it.
func TestTemplatesHaveNoInlineScript(t *testing.T) {
	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	pages, err := fs.Glob(web.TemplateFS, "templates/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, name := range pages {
		b, err := fs.ReadFile(web.TemplateFS, name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, tag := range scriptTag.FindAllString(src, -1) {
			if !strings.Contains(tag, ` src="`) && tag != `<script type="application/json" id="page-data">` {
				t.Errorf("%s: inline script %s", name, tag)
			}
		}
		// Only inside tags: prose may well say "turn on=".
		for _, tag := range regexp.MustCompile(`<[a-z][^>]*>`).FindAllString(src, -1) {
			if handler.MatchString(tag) {
				t.Errorf("%s: inline event handler in %s", name, tag)
			}
		}
		if strings.Contains(src, "javascript:") {
			t.Errorf("%s: javascript: URL", name)
		}
	}
}

// The profile's edit entry hands its values to the script in data
// attributes, decoded by the browser into the bare values.
func TestAdminEditEntryCarriesTheProfile(t *testing.T) {
	var buf bytes.Buffer
	err := parseAdminTemplate().Execute(&buf, adminData{
		Profiles: []database.Profile{{ID: 2, Name: `say "hi"`, Description: "a desc",
			Models: []string{"m1", "m2"},
			Quotas: []database.ProfileQuota{{Budget: 5, Period: "30d"}}}},
		Users:     []userRow{},
		CSRFToken: "TOK",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-id="2"`,
		`data-name="say &#34;hi&#34;"`,
		`data-models="m1,m2"`,
		`data-rpm=""`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("edit entry lacks %s", want)
		}
	}
	m := regexp.MustCompile(`data-quotas="([^"]*)"`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("edit entry lacks data-quotas")
	}
	var quotas []database.ProfileQuota
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &quotas); err != nil {
		t.Fatalf("data-quotas is not JSON: %v", err)
	}
	if len(quotas) != 1 || quotas[0].Budget != 5 || quotas[0].Period != "30d" {
		t.Errorf("quotas = %+v", quotas)
	}
	if strings.Contains(out, "data-default") {
		t.Error("a profile that is not the default is marked as the default")
	}
}
