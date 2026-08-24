package cmd

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// version is only filled in by `-ldflags "-X github.com/hugo-andrade/avellcc/cmd.version=v1.2.3"`.
// It is empty by default on purpose: the hardcoded "0.2.0" that used to live
// here stayed put while the tags moved to v0.3.0, so `avellcc -v` could not
// tell apart any build made since 0.2.0 — answering "is the installed binary
// this code?" meant comparing sha256 of builds by hand. What answers now is
// the module stamp the toolchain writes into the binary, which nobody has to
// remember to bump.
var version = ""

// resolveVersion reports the module version stamped into this binary.
//
// `go build` inside the repository stamps Main.Version from the VCS tag plus
// the commit and a `+dirty` marker; `go run` and `go test` do not stamp a
// version at all (Main.Version is "(devel)"), and there the revision — when
// there is one — is the useful answer.
func resolveVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, modified := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	var b strings.Builder
	b.WriteString("devel-")
	b.WriteString(rev[:min(12, len(rev))])
	if modified {
		b.WriteString("+dirty")
	}
	return b.String()
}

var rootCmd = &cobra.Command{
	Use:   "avellcc",
	Short: "Avell Storm 590X Control Center for Linux",
	Long:  "Control keyboard RGB, lightbar, and fans on Avell/Clevo laptops.",
	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Version = resolveVersion()
}
