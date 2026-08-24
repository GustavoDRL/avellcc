package cmd

import (
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
)

// captureStdout runs fn with os.Stdout replaced by a pipe and returns what it
// printed. showLightbarConfig writes with fmt.Printf, which resolves os.Stdout
// at call time, so swapping the variable is enough.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		body, _ := io.ReadAll(r)
		done <- string(body)
	}()

	runErr := fn()
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("showLightbarConfig: %v\noutput so far:\n%s", runErr, out)
	}
	return out
}

// `avellcc lightbar --show-config` printed [theme] and [pulse] and stopped, so
// the three [keyboard] values that live in the same file could only be found by
// opening it. Driven off the struct's toml tags: a fourth field added tomorrow
// without a fourth Printf turns this red instead of shipping invisible.
func TestShowConfigPrintsEveryKeyboardKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out := captureStdout(t, func() error {
		return showLightbarConfig(&cobra.Command{})
	})

	header := strings.Index(out, "[keyboard]")
	if header < 0 {
		t.Fatalf("--show-config never prints a [keyboard] section:\n%s", out)
	}
	section := out[header:]

	typ := reflect.TypeOf(config.KeyboardSettings{})
	for i := 0; i < typ.NumField(); i++ {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("toml"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("%s has no toml tag", typ.Field(i).Name)
		}
		if !strings.Contains(section, tag+" ") {
			t.Errorf("keyboard.%s is in lightbar.toml but --show-config never "+
				"prints it; add a line to showLightbarConfig", tag)
		}
	}
}

// The values printed have to be the ones in force, not the defaults: a section
// that always shows 8 and "accent" is worse than no section, because it looks
// like an answer.
func TestShowConfigPrintsTheKeyboardValuesInForce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(dir+"/avellcc", 0o755); err != nil {
		t.Fatal(err)
	}
	// The shipped file already carries a [keyboard] table, so this edits it in
	// place; appending a second one would be invalid TOML. The newlines around
	// `brightness = 8` matter — without them the pattern also matches the
	// theme's `brightness = 80`.
	body := config.DefaultLightbarSettingsFile
	for _, r := range [][2]string{
		{"[keyboard]\nenabled = true", "[keyboard]\nenabled = false"},
		{"\nbrightness = 8\n", "\nbrightness = 2\n"},
		{"\ncolor_key = \"accent\"\n", "\ncolor_key = \"bright_magenta\"\n"},
	} {
		replaced := strings.Replace(body, r[0], r[1], 1)
		if replaced == body {
			t.Fatalf("the shipped default file no longer contains %q; "+
				"this fixture has gone stale", r[0])
		}
		body = replaced
	}
	if err := os.WriteFile(config.LightbarSettingsPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Proof the fixture is what the test thinks it is, before asserting on the
	// printed form of it.
	settings, err := config.LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Keyboard.Enabled || settings.Keyboard.Brightness != 2 ||
		settings.Keyboard.ColorKey != "bright_magenta" {
		t.Fatalf("the fixture did not take: %+v", settings.Keyboard)
	}

	out := captureStdout(t, func() error {
		return showLightbarConfig(&cobra.Command{})
	})
	header := strings.Index(out, "[keyboard]")
	if header < 0 {
		t.Fatalf("--show-config never prints a [keyboard] section:\n%s", out)
	}
	section := out[header:]
	for _, want := range []string{"enabled        = false", "brightness     = 2",
		`color_key      = "bright_magenta"`} {
		if !strings.Contains(section, want) {
			t.Errorf("--show-config does not report %q:\n%s", want, section)
		}
	}
	// A disabled section has to reach the summary too, or the summary reads as
	// "keyboard is on".
	if !strings.Contains(out, "Disabled:") || !strings.Contains(out, "keyboard") {
		t.Errorf("a disabled keyboard is missing from the Disabled line:\n%s", out)
	}
}
