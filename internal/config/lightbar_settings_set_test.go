package config

import (
	"os"
	"strings"
	"testing"
)

// Preserving comments is the entire reason this writer is line-oriented
// instead of a re-encode, so it is the thing worth asserting hardest: the
// comments carry the ranges and the reason behind each default, and a file
// that loses them is much worse than one that is untidy.
func TestWritePreservesCommentsAndLayout(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	if err := WriteLightbarSetting("pulse.fps", "45"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(LightbarSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	if !strings.Contains(got, "fps = 45") {
		t.Errorf("the value was not written:\n%s", got)
	}
	for _, comment := range []string{
		"# Chassis light bar",
		"# Frame rate, and cava's. Measured clean to 60 on this controller.",
		"# Brightness between beats. Not 0: a bar that goes fully dark reads as broken.",
		"# cava's capture. \"auto\" follows the default sink's monitor.",
	} {
		if !strings.Contains(got, comment) {
			t.Errorf("comment lost: %q", comment)
		}
	}
	if strings.Contains(got, "fps = 30") {
		t.Error("the old value is still in the file")
	}

	// And the result still has to load.
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Pulse.FPS != 45 {
		t.Errorf("reloaded fps = %d, want 45", settings.Pulse.FPS)
	}
}

// Two sections can hold the same key name. `enabled` exists in both, and
// writing one must not touch the other.
func TestWriteTargetsTheRightSection(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	if err := WriteLightbarSetting("pulse.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Pulse.Enabled {
		t.Error("pulse.enabled was not written")
	}
	if !settings.Theme.Enabled {
		t.Error("theme.enabled was changed by a write to pulse.enabled")
	}
}

// A trailing comment documents the setting, not the value it happened to have.
func TestWriteKeepsATrailingComment(t *testing.T) {
	withSettingsFile(t, "[pulse]\nfps = 30  # frames per second\n")

	if err := WriteLightbarSetting("pulse.fps", "60"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(LightbarSettingsPath())
	if got := strings.TrimSpace(string(body)); got != "[pulse]\nfps = 60  # frames per second" {
		t.Errorf("got:\n%s", got)
	}
}

// A file that predates a setting, or that a user trimmed, still has to accept
// it — and the key has to land in its own section, not after the file's last
// line, which would file it under whichever section happens to be last.
func TestWriteAddsAMissingKeyInsideItsSection(t *testing.T) {
	withSettingsFile(t, "[theme]\nbrightness = 40\n\n[pulse]\nfps = 30\n")

	if err := WriteLightbarSetting("theme.speed", "7"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme.Speed != 7 {
		t.Errorf("theme.speed = %d, want 7", settings.Theme.Speed)
	}
	if settings.Pulse.FPS != 30 {
		t.Errorf("pulse.fps = %d, want 30 — the key landed in the wrong section", settings.Pulse.FPS)
	}
}

func TestWriteCreatesAMissingSection(t *testing.T) {
	withSettingsFile(t, "[theme]\nbrightness = 40\n")

	if err := WriteLightbarSetting("pulse.gain", "1.5"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Pulse.Gain != 1.5 {
		t.Errorf("pulse.gain = %g, want 1.5", settings.Pulse.Gain)
	}
}

// A rejected value must never reach the disk, or a panel slider dragged past
// a limit would leave the file unloadable.
func TestRejectedValueLeavesTheFileUntouched(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)
	before, _ := os.ReadFile(LightbarSettingsPath())

	if err := WriteLightbarSetting("pulse.fps", "0"); err == nil {
		t.Fatal("an out-of-range value was accepted")
	}
	if err := WriteLightbarSetting("theme.effect", "disco"); err == nil {
		t.Fatal("an unknown effect was accepted")
	}
	if err := WriteLightbarSetting("pulse.enabled", "yesplease"); err == nil {
		t.Fatal("a non-boolean was accepted")
	}

	after, _ := os.ReadFile(LightbarSettingsPath())
	if string(before) != string(after) {
		t.Error("the file changed despite every write being rejected")
	}
}

// A cross-field rule can only be checked against the whole object, and a
// rejection has to leave the in-memory settings as they were.
func TestCrossFieldRejectionRollsBack(t *testing.T) {
	settings := DefaultLightbarSettings()
	if err := SetLightbarSetting(&settings, "pulse.min_brightness", "150"); err == nil {
		t.Fatal("min above max was accepted")
	}
	if settings.Pulse.MinBrightness != DefaultLightbarSettings().Pulse.MinBrightness {
		t.Errorf("a rejected set left %d behind", settings.Pulse.MinBrightness)
	}
}

func TestUnknownKeyListsTheRealOnes(t *testing.T) {
	settings := DefaultLightbarSettings()
	err := SetLightbarSetting(&settings, "pulse.speed", "3")
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if !strings.Contains(err.Error(), "pulse.fps") {
		t.Errorf("the error does not list the real keys: %v", err)
	}
}

// Every key has to be settable and readable, or the panel would offer a
// control that silently does nothing.
func TestEveryKeyRoundTrips(t *testing.T) {
	values := map[string]string{
		"theme.enabled":        "false",
		"theme.brightness":     "55",
		"theme.effect":         "breathing",
		"theme.speed":          "3",
		"theme.color_key":      "bright_magenta",
		"pulse.enabled":        "false",
		"pulse.fps":            "45",
		"pulse.min_brightness": "20",
		"pulse.max_brightness": "90",
		"pulse.gain":           "1.5",
		"pulse.player":         "mpv",
		"pulse.input_method":   "pulse",
		"pulse.input_source":   "default",
		"keyboard.enabled":     "false",
		"keyboard.brightness":  "3",
		"keyboard.color_key":   "bright_magenta",
	}
	if len(values) != len(SettingKeys()) {
		t.Fatalf("this test covers %d keys but there are %d: %v",
			len(values), len(SettingKeys()), SettingKeys())
	}

	withSettingsFile(t, DefaultLightbarSettingsFile)
	for key, want := range values {
		if err := WriteLightbarSetting(key, want); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range values {
		got, err := GetLightbarSetting(settings, key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if strings.Trim(got, `"`) != want {
			t.Errorf("%s round-tripped to %s, want %s", key, got, want)
		}
	}
}
