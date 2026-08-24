package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/config"
)

// `keyboard --theme` and `lightbar --theme` have to resolve the *same* rule.
// The owner's decision is that the wallpaper decides the keyboard's colour, and
// the wallpaper reaches avellcc as the accent override — so the moment the two
// commands stop sharing themeColor, one of them stops honouring it. That is not
// hypothetical: it is exactly what the sed picker inside 50-avellcc-keyboard
// did, writing the theme's #FFB6D1 for the three seconds before the now-playing
// integration replaced it with the wallpaper's #8AA4B0.

const themeColorsBody = `
accent = "#FFB6D1"
red = "#FF4848"
green = "#6E8F7A"
blue = "#8AA4B0"
cyan = "#7FD1DE"
yellow = "#F5C77E"
magenta = "#FFB6D1"
bright_red = "#FF6B6B"
bright_green = "#8FBF9F"
bright_yellow = "#FFD79A"
bright_blue = "#A7C4D0"
bright_cyan = "#9FE4EE"
bright_magenta = "#FFC9DE"
`

// withAppliedTheme plants a theme, an override path and a settings file in a
// throwaway HOME. Nothing here reads or writes the real ones.
func withAppliedTheme(t *testing.T, settingsBody string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	dir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(themeColorsBody), 0o644); err != nil {
		t.Fatal(err)
	}

	override := filepath.Join(home, "accent-override")
	t.Setenv("AVELLCC_ACCENT_OVERRIDE", override)

	if settingsBody != "" {
		if err := os.MkdirAll(config.ConfigDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.LightbarSettingsPath(), []byte(settingsBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return override
}

// The G09 case: with color_key set to a named key, the override was skipped —
// including when the name was literally "accent".
func TestANamedColourKeyHonoursTheAccentOverride(t *testing.T) {
	override := withAppliedTheme(t, "")
	if err := os.WriteFile(override, []byte("#8AA4B0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := themeColor("accent")
	if err != nil {
		t.Fatal(err)
	}
	if got != "#8aa4b0" {
		t.Errorf(`themeColor("accent") = %s, want the override #8aa4b0; `+
			`asking for the accent by name must not be a way to lose it`, got)
	}

	// A key that is not the accent is the theme's own and does not move.
	if got, err := themeColor("red"); err != nil || got != "#ff4848" {
		t.Errorf(`themeColor("red") = %s, %v; want #ff4848`, got, err)
	}
}

// The two --theme flags must answer the same thing for the same key. They share
// one resolver, and this is what says so out loud.
func TestKeyboardAndLightbarThemeResolveTheSameColour(t *testing.T) {
	for _, key := range []string{"accent", "auto", "blue", "bright_magenta"} {
		t.Run(key, func(t *testing.T) {
			override := withAppliedTheme(t, "[keyboard]\ncolor_key = \""+key+"\"\n"+
				"[theme]\ncolor_key = \""+key+"\"\n")
			if err := os.WriteFile(override, []byte("#8AA4B0"), 0o644); err != nil {
				t.Fatal(err)
			}

			settings, err := config.LoadLightbarSettings()
			if err != nil {
				t.Fatal(err)
			}
			// The light bar's half, exactly as runLightbar8233 calls it.
			wantLightbar, err := themeColor(settings.Theme.ColorKey)
			if err != nil {
				t.Fatal(err)
			}

			// The keyboard's half, through the command's own resolution.
			kbColor, kbBrightness, kbBrightSet = "", 0, false
			enabled, err := applyKeyboardThemeFlags()
			if err != nil {
				t.Fatal(err)
			}
			if !enabled {
				t.Fatal("the keyboard half is enabled by default and reported disabled")
			}
			if kbColor != wantLightbar {
				t.Errorf("keyboard --theme resolved %s but lightbar --theme resolved %s "+
					"for color_key=%q", kbColor, wantLightbar, key)
			}
		})
	}
}

// The brightness the hook used to carry as an undocumented environment variable
// now comes from the file, and an explicit --brightness still wins.
func TestKeyboardThemeTakesTheBrightnessFromTheFile(t *testing.T) {
	withAppliedTheme(t, "[keyboard]\nbrightness = 3\n")

	kbColor, kbBrightness, kbBrightSet = "", 0, false
	if _, err := applyKeyboardThemeFlags(); err != nil {
		t.Fatal(err)
	}
	if !kbBrightSet || kbBrightness != 3 {
		t.Errorf("brightness = %d (set=%v), want 3 from the file", kbBrightness, kbBrightSet)
	}

	kbColor, kbBrightness, kbBrightSet = "", 9, true
	if _, err := applyKeyboardThemeFlags(); err != nil {
		t.Fatal(err)
	}
	if kbBrightness != 9 {
		t.Errorf("brightness = %d, want the 9 the user typed", kbBrightness)
	}
}

// Turning the keyboard half off has to be a silent success: the hook runs on
// every theme switch and must not start failing because of a setting.
func TestKeyboardThemeDisabledIsASilentSuccess(t *testing.T) {
	withAppliedTheme(t, "[keyboard]\nenabled = false\n")

	kbColor = ""
	enabled, err := applyKeyboardThemeFlags()
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("enabled = true with keyboard.enabled = false in the file")
	}
	if kbColor != "" {
		t.Errorf("a disabled keyboard half still resolved a colour: %s", kbColor)
	}
}

// The default is "accent" and not "auto" on purpose: the accent is the entry
// the wallpaper overrides, and the owner's rule is that the wallpaper decides.
func TestTheKeyboardDefaultsToTheAccentSoTheWallpaperDecides(t *testing.T) {
	override := withAppliedTheme(t, "")
	if err := os.WriteFile(override, []byte("#8AA4B0"), 0o644); err != nil {
		t.Fatal(err)
	}

	kbColor = ""
	if _, err := applyKeyboardThemeFlags(); err != nil {
		t.Fatal(err)
	}
	if kbColor != "#8aa4b0" {
		t.Errorf("keyboard --theme resolved %s, want the wallpaper's #8aa4b0", kbColor)
	}
}
