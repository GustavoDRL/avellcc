package cmd

import (
	"runtime/debug"
	"strings"
	"testing"
)

// The version this binary reports has to come from the build, not from a
// literal somebody has to remember to bump. `var version = "0.2.0"` sat here
// while the tags went to v0.3.0, so `-v` answered the same string for every
// build made in between.
//
// The test compares resolveVersion() against what the running test binary
// actually carries, so it stays true under `go test` (no module version, no
// VCS settings) and under a stamped `go build` alike. Putting a literal back
// makes it red on the spot.
func TestVersionComesFromTheBuildAndNotFromALiteral(t *testing.T) {
	// Deliberately NOT overriding `version` here: the literal in the package is
	// exactly what this test exists to catch. An earlier draft set it to "" for
	// the duration of the test, and stayed green with `var version = "0.2.0"`
	// put back — it was measuring its own setup.
	got := resolveVersion()

	want := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			want = v
		} else {
			rev, modified := "", false
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					modified = s.Value == "true"
				}
			}
			if rev != "" {
				want = "devel-" + rev[:min(12, len(rev))]
				if modified {
					want += "+dirty"
				}
			}
		}
	}

	if got != want {
		t.Fatalf("resolveVersion() = %q, but this build carries %q", got, want)
	}
	if strings.HasPrefix(got, "0.") {
		t.Fatalf("resolveVersion() = %q — that is the shape of the hardcoded literal, "+
			"which cannot tell two builds apart", got)
	}
}

// The -ldflags override still has to win: it is how a release stamps the tag.
func TestLdflagsVersionWins(t *testing.T) {
	saved := version
	version = "v9.9.9-from-ldflags"
	t.Cleanup(func() { version = saved })

	if got := resolveVersion(); got != "v9.9.9-from-ldflags" {
		t.Fatalf("resolveVersion() = %q, want the -X value", got)
	}
}
