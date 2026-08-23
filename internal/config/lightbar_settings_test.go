package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSettingsFile points ConfigDir at a temp directory holding body, or at an
// empty one when body is empty.
func withSettingsFile(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if body == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "avellcc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "avellcc", "lightbar.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// No file is not an error. It has to mean "every default applies", or the
// theme hook would start failing on a machine that never wrote one.
func TestMissingFileGivesDefaults(t *testing.T) {
	withSettingsFile(t, "")
	got, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultLightbarSettings() {
		t.Errorf("missing file gave %+v, want the defaults", got)
	}
}

// A partial file overrides only what it names.
func TestPartialFileLeavesTheRestAtDefaults(t *testing.T) {
	withSettingsFile(t, "[pulse]\nfps = 60\nmin_brightness = 25\n")
	got, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Pulse.FPS != 60 || got.Pulse.MinBrightness != 25 {
		t.Errorf("file values not applied: %+v", got.Pulse)
	}
	defaults := DefaultLightbarSettings()
	if got.Pulse.Gain != defaults.Pulse.Gain || got.Theme != defaults.Theme {
		t.Errorf("unnamed settings drifted from the defaults: %+v", got)
	}
}

// The whole point of a settings file is that a user edits it by hand, so a
// misspelled key has to be reported rather than silently ignored — otherwise
// the user stares at a setting that visibly does nothing.
func TestUnknownKeyIsReportedByName(t *testing.T) {
	withSettingsFile(t, "[pulse]\nmin_brigthness = 25\n")
	_, err := LoadLightbarSettings()
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "min_brigthness") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestInvalidValuesAreRejectedOnLoad(t *testing.T) {
	cases := map[string]string{
		"unknown effect":    "[theme]\neffect = \"disco\"\n",
		"brightness range":  "[theme]\nbrightness = 300\n",
		"speed range":       "[theme]\nspeed = 99\n",
		"fps range":         "[pulse]\nfps = 0\n",
		"inverted range":    "[pulse]\nmin_brightness = 90\nmax_brightness = 10\n",
		"non-positive gain": "[pulse]\ngain = 0\n",
		"empty player":      "[pulse]\nplayer = \"\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			withSettingsFile(t, body)
			if _, err := LoadLightbarSettings(); err == nil {
				t.Errorf("accepted an invalid setting")
			}
		})
	}
}

// Writing `player = "spotify"` should not require knowing the D-Bus naming
// convention, and a full bus name still has to pass through untouched.
func TestMPRISNameExpandsShortNamesOnly(t *testing.T) {
	cases := map[string]string{
		"spotify":                        "org.mpris.MediaPlayer2.spotify",
		"mpv":                            "org.mpris.MediaPlayer2.mpv",
		"org.mpris.MediaPlayer2.firefox": "org.mpris.MediaPlayer2.firefox",
	}
	for in, want := range cases {
		if got := (PulseSettings{Player: in}).MPRISName(); got != want {
			t.Errorf("MPRISName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The shipped commented file has to be a file the loader accepts — it is the
// first thing a user sees, and a default that fails validation is worse than
// no default at all.
func TestShippedDefaultFileLoadsAndMatchesTheDefaults(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)
	got, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultLightbarSettings() {
		t.Errorf("the shipped file parses to %+v, which is not the defaults %+v",
			got, DefaultLightbarSettings())
	}
}

// It is written once, so an upgrade never clobbers a user's edits.
func TestDefaultFileIsWrittenOnlyWhenAbsent(t *testing.T) {
	withSettingsFile(t, "")
	wrote, err := WriteDefaultLightbarSettingsFile()
	if err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	if err := os.WriteFile(LightbarSettingsPath(), []byte("[pulse]\nfps = 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err = WriteDefaultLightbarSettingsFile()
	if err != nil || wrote {
		t.Fatalf("second write: wrote=%v err=%v, want it to leave the file alone", wrote, err)
	}
	body, _ := os.ReadFile(LightbarSettingsPath())
	if !strings.Contains(string(body), "fps = 60") {
		t.Error("the existing file was overwritten")
	}
}
