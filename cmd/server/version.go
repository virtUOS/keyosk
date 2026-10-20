package main

import (
	"fmt"
	"io"

	"github.com/virtuos/keyosk/internal/buildinfo"
)

// writeVersion prints the build for -version, one fact per line, leaving out
// what the build did not record.
func writeVersion(w io.Writer, info buildinfo.Info) {
	fmt.Fprintf(w, "keyosk %s\n", info.Version)
	if info.Commit != "" {
		modified := ""
		if info.Modified {
			modified = " (modified)"
		}
		fmt.Fprintf(w, "commit %s%s\n", info.Commit, modified)
	}
	if !info.Date.IsZero() {
		fmt.Fprintf(w, "date   %s\n", info.Date.UTC().Format("2006-01-02 15:04 UTC"))
	}
	if info.GoVersion != "" {
		fmt.Fprintf(w, "go     %s\n", info.GoVersion)
	}
}

// versionAttrs are the log fields that name the build, logged at startup so
// every log begins with what was running.
func versionAttrs(info buildinfo.Info) []any {
	attrs := []any{"version", info.Version}
	if info.Commit != "" {
		attrs = append(attrs, "commit", info.ShortCommit())
	}
	if info.Modified {
		attrs = append(attrs, "modified", true)
	}
	return attrs
}
