package buildinfo

import (
	"runtime/debug"
	"testing"
	"time"
)

func TestFromBuildInfo(t *testing.T) {
	info := fromBuildInfo(&debug.BuildInfo{
		GoVersion: "go1.27.0",
		Main:      debug.Module{Version: "v0.8.2-0.20261020120000-0123456789ab+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "-trimpath", Value: "true"},
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.time", Value: "2026-10-20T12:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	})
	want := Info{
		Version:   "v0.8.2-0.20261020120000-0123456789ab+dirty",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		Date:      time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Modified:  true,
		GoVersion: "go1.27.0",
	}
	if info != want {
		t.Errorf("got %+v, want %+v", info, want)
	}
	if got := info.ShortCommit(); got != "0123456789ab" {
		t.Errorf("short commit %q", got)
	}
}

// A build without .git records no VCS settings; the fields stay empty rather
// than holding something made up.
func TestFromBuildInfoWithoutVCS(t *testing.T) {
	info := fromBuildInfo(&debug.BuildInfo{GoVersion: "go1.27.0", Main: debug.Module{Version: "(devel)"}})
	if info.Commit != "" || !info.Date.IsZero() || info.Modified || info.ShortCommit() != "" {
		t.Errorf("got %+v, want no VCS fields", info)
	}
	if info.Version != "(devel)" || info.GoVersion != "go1.27.0" {
		t.Errorf("got %+v", info)
	}
}
