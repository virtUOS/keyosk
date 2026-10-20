package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/virtuos/keyosk/internal/buildinfo"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/web"
)

// Where the project lives. Fixed rather than configurable: a deployment runs
// Keyosk, and these are Keyosk's.
const (
	projectURL = "https://github.com/virtUOS/keyosk"
	issuesURL  = projectURL + "/issues"
	docsURL    = "https://virtuos.github.io/keyosk/"
)

func parseAboutTemplate() *template.Template {
	return template.Must(template.New("about.html").
		Funcs(langFuncs()).
		ParseFS(web.TemplateFS, "templates/about.html", "templates/layout.html"))
}

// aboutData is what about.html renders.
type aboutData struct {
	Lang      i18n.Lang
	Theme     Theme
	Path      string
	CSRFToken string
	TitleKey  string
	// User, IsAdmin and PrivacyNotice feed the shared header and footer.
	User          *database.User
	IsAdmin       bool
	PrivacyNotice bool

	Build buildinfo.Info
	// CommitURL points to the commit on the project page, empty when the
	// build recorded none.
	CommitURL  string
	DocsURL    string
	IssuesURL  string
	ProjectURL string
}

// About renders what Keyosk is, which build of it runs here, and where to read
// about it or report a problem.
//
// It needs a login, like the menu that links it: the exact version would tell
// anyone looking for an unpatched portal which one this is.
func (u *UI) About(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := u.aboutTmpl.Execute(w, newAboutData(buildinfo.Get(), aboutData{
		Lang:          i18n.FromRequest(r),
		Theme:         ThemeFromRequest(r),
		Path:          r.URL.Path,
		CSRFToken:     u.csrf.Token(w, r),
		TitleKey:      "about.title",
		User:          su.User,
		IsAdmin:       su.User.IsAdmin,
		PrivacyNotice: hasPrivacyNotice(u.cfg),
	})); err != nil {
		slog.Error("about template", "err", err)
	}
}

// newAboutData adds the build and the project's links to the page chrome in d.
func newAboutData(build buildinfo.Info, d aboutData) aboutData {
	d.Build = build
	if build.Commit != "" {
		d.CommitURL = projectURL + "/commit/" + build.Commit
	}
	d.DocsURL, d.IssuesURL, d.ProjectURL = docsURL, issuesURL, projectURL
	return d
}
