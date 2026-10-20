// Package buildinfo reports which build of Keyosk is running.
//
// Nothing is set at build time by hand: the go command stamps the module
// version and the commit into the binary when it builds from a git checkout,
// and this package reads them back. A build from the tag v1.2.3 reports
// v1.2.3; a later commit reports a pseudo-version such as
// v1.2.4-0.20261020123456-abcdef123456, and uncommitted changes add +dirty.
// Without .git, as from a source tarball, only (devel) is known.
//
// A build from a git worktree inside another checkout reports that
// checkout's commit, not the worktree's: the go command takes only a .git
// directory for the root of a repository and walks up past the worktree's
// .git file.
package buildinfo

import (
	"runtime/debug"
	"sync"
	"time"
)

// Info describes the running binary. Fields the build did not record are
// empty.
type Info struct {
	// Version is the module version, e.g. v1.2.3 or a pseudo-version, or
	// "(devel)" when the go command could not tell.
	Version string
	// Commit is the full revision of the checkout the binary was built from.
	Commit string
	// Date is the commit time of that revision.
	Date time.Time
	// Modified reports that the checkout had uncommitted changes.
	Modified bool
	// GoVersion is the toolchain the binary was built with.
	GoVersion string
}

// ShortCommit is the commit cut to the twelve characters a pseudo-version
// uses, enough to find it in the repository.
func (i Info) ShortCommit() string {
	if len(i.Commit) > 12 {
		return i.Commit[:12]
	}
	return i.Commit
}

// Get reports the build of the running binary.
var Get = sync.OnceValue(func() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{}
	}
	return fromBuildInfo(bi)
})

// fromBuildInfo picks the fields of Info out of what the go command recorded.
func fromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{Version: bi.Main.Version, GoVersion: bi.GoVersion}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				info.Date = t
			}
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}
