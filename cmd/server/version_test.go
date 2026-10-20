package main

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/buildinfo"
)

func TestWriteVersion(t *testing.T) {
	var out bytes.Buffer
	writeVersion(&out, buildinfo.Info{
		Version:   "v0.8.1",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		Date:      time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Modified:  true,
		GoVersion: "go1.27.0",
	})
	want := "keyosk v0.8.1\n" +
		"commit 0123456789abcdef0123456789abcdef01234567 (modified)\n" +
		"date   2026-10-20 12:00 UTC\n" +
		"go     go1.27.0\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// A build without .git prints only what it knows.
func TestWriteVersionWithoutVCS(t *testing.T) {
	var out bytes.Buffer
	writeVersion(&out, buildinfo.Info{Version: "(devel)", GoVersion: "go1.27.0"})
	if want := "keyosk (devel)\ngo     go1.27.0\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestVersionAttrs(t *testing.T) {
	got := versionAttrs(buildinfo.Info{Version: "v0.8.1", Commit: "0123456789abcdef", Modified: true})
	want := []any{"version", "v0.8.1", "commit", "0123456789ab", "modified", true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
