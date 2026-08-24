package config

import (
	"strings"
	"testing"
)

// The keyboard's three settings used to be environment variables read inside
// 50-avellcc-keyboard, which meant they existed only for whoever read the hook.
// They now ride in the same file as the light bar's.

func TestKeyboardDefaultsReproduceTheOldHook(t *testing.T) {
	k := DefaultKeyboardSettings()
	// The hook's defaults were AVELLCC_THEME_COLOR_KEY=accent and
	// AVELLCC_THEME_BRIGHTNESS=8. Installing this must not move anybody's
	// keyboard on its own.
	if !k.Enabled || k.Brightness != 8 || k.ColorKey != "accent" {
		t.Errorf("defaults = %+v, want enabled, brightness 8, color_key accent", k)
	}
}

// A file written before [keyboard] existed still has to load — that is every
// file on every machine that has this installed today.
func TestAFileWithoutTheKeyboardSectionStillLoads(t *testing.T) {
	got, err := DecodeLightbarSettings("[theme]\nenabled = false\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Keyboard != DefaultKeyboardSettings() {
		t.Errorf("keyboard = %+v, want the defaults", got.Keyboard)
	}
	if got.Theme.Enabled {
		t.Error("the theme section was not read")
	}
}

func TestKeyboardSectionIsRead(t *testing.T) {
	got, err := DecodeLightbarSettings("[keyboard]\nenabled = false\nbrightness = 2\n" +
		"color_key = \"bright_magenta\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := KeyboardSettings{Enabled: false, Brightness: 2, ColorKey: "bright_magenta"}
	if got.Keyboard != want {
		t.Errorf("keyboard = %+v, want %+v", got.Keyboard, want)
	}
}

// Validation happens on load, with the file named, rather than later as a
// confusing HID error or a colour that silently does nothing.
func TestInvalidKeyboardValuesAreRejectedOnLoad(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"[keyboard]\nbrightness = 11\n", "keyboard.brightness"},
		{"[keyboard]\nbrightness = -1\n", "keyboard.brightness"},
		{"[keyboard]\ncolor_key = \"\"\n", "keyboard.color_key"},
	} {
		_, err := DecodeLightbarSettings(tc.body)
		if err == nil {
			t.Errorf("%q was accepted", tc.body)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %q does not name %s", tc.body, err, tc.want)
		}
	}
}

// A misspelling inside the new section has to be reported by name like every
// other key, not ignored into a setting that visibly does nothing.
func TestAMisspelledKeyboardKeyIsReported(t *testing.T) {
	_, err := DecodeLightbarSettings("[keyboard]\nbrightnes = 3\n")
	if err == nil || !strings.Contains(err.Error(), "brightnes") {
		t.Errorf("err = %v, want the unknown key named", err)
	}
}

// The shipped file is the first thing a user sees, and the section has to be
// in it or the settings stay as undiscoverable as the environment variables
// they replaced.
func TestTheShippedFileDocumentsTheKeyboardSection(t *testing.T) {
	for _, want := range []string{"[keyboard]", "\nenabled = true", "\nbrightness = 8",
		"\ncolor_key = \"accent\""} {
		if !strings.Contains(DefaultLightbarSettingsFile, want) {
			t.Errorf("the shipped default file does not contain %q", want)
		}
	}
}
