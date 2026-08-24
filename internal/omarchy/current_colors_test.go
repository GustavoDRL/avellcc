package omarchy

import (
	"os"
	"path/filepath"
	"testing"
)

// The accent override is how something outside avellcc says "the colour right
// now is this one" — on this machine it is the now-playing integration parking
// the wallpaper's colour there. It used to be applied only inside
// CurrentPalette, so every caller that indexed colors.toml by key never saw it:
// `color_key = "accent"`, the user asking for the accent in as many words, got
// the file's value while `color_key = "auto"` followed the override.

// bmthColors is the applied theme on the machine this was measured on.
const bmthColors = `
accent = "#FFB6D1"
red = "#FF4848"
green = "#6E8F7A"
yellow = "#F5C77E"
blue = "#8AA4B0"
cyan = "#7FD1DE"
magenta = "#FFB6D1"
bright_red = "#FF6B6B"
bright_green = "#8FBF9F"
bright_yellow = "#FFD79A"
bright_blue = "#A7C4D0"
bright_cyan = "#9FE4EE"
bright_magenta = "#FFC9DE"
mode = "dark"
`

// withTheme plants an applied theme in a throwaway HOME and points the override
// at a path inside it, so nothing here can read or write the real one.
func withTheme(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(home, "accent-override")
	t.Setenv("AVELLCC_ACCENT_OVERRIDE", override)
	return override
}

func TestCurrentColorsAppliesTheAccentOverride(t *testing.T) {
	override := withTheme(t, bmthColors)

	colors, err := CurrentColors()
	if err != nil {
		t.Fatal(err)
	}
	if got := colors["accent"].Hex(); got != "#ffb6d1" {
		t.Fatalf("with no override, accent = %s, want the theme's #ffb6d1", got)
	}

	if err := os.WriteFile(override, []byte("#8AA4B0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	colors, err = CurrentColors()
	if err != nil {
		t.Fatal(err)
	}
	if got := colors["accent"].Hex(); got != "#8aa4b0" {
		t.Errorf("accent = %s, want the override #8aa4b0 — a caller indexing this "+
			"map by key would be ignoring the override", got)
	}
	// The other keys are the theme's own and must not move with it.
	if got := colors["red"].Hex(); got != "#ff4848" {
		t.Errorf("red = %s, want #ff4848", got)
	}
}

// CurrentPalette is now built on CurrentColors, so this pins that the override
// did not stop working on the path that already had it.
func TestCurrentPaletteStillFollowsTheOverride(t *testing.T) {
	override := withTheme(t, bmthColors)
	if err := os.WriteFile(override, []byte("#8AA4B0"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := CurrentPalette()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Bass.Hex(); got != "#8aa4b0" {
		t.Errorf("bass = %s, want the override #8aa4b0", got)
	}
}
