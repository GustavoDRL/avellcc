package config

import (
	"fmt"

	"github.com/hugo-andrade/avellcc/internal/keyboard"
)

// KeyboardSettings is what the keyboard backlight shows at rest: the colour the
// theme-set hook writes after every `omarchy theme set`.
//
// It lives in lightbar.toml, next to [theme] and [pulse], because there is one
// file for the whole integration and a second one would be a second place to
// look. The section is [keyboard] rather than [theme] so the two devices can be
// turned off independently — this machine's owner keeps the light bar off and
// the keyboard on.
//
// These three values used to be undocumented environment variables read by the
// hook — AVELLCC_THEME_COLOR_KEY, AVELLCC_THEME_BRIGHTNESS and
// AVELLCC_THEME_USE_KEYBOARD_RGB. A tunable that only exists inside a hook
// script is a tunable nobody finds, and a file is at least a place to look.
//
// THAT ARGUMENT IS FINISHED, and both human surfaces now hold it up:
// showLightbarConfig (cmd/lightbar_settings.go) prints [keyboard] field by
// field alongside [theme] and [pulse], and settingFields() in
// lightbar_settings_set.go carries keyboard.enabled, keyboard.brightness and
// keyboard.color_key, so `avellcc lightbar config set keyboard.brightness 3`
// works. Neither is left to good intentions: the tests are driven by reflection
// over the toml tags of this struct, so a FOURTH field added here without a
// Printf there and an entry in settingFields() turns them red instead of
// quietly becoming another value that only exists inside a file. `config show
// --json` never had the gap — it serialises this struct whole.
type KeyboardSettings struct {
	Enabled    bool   `toml:"enabled" json:"enabled"`
	Brightness int    `toml:"brightness" json:"brightness"`
	ColorKey   string `toml:"color_key" json:"color_key"`
}

// DefaultKeyboardSettings reproduces what the hook did with no environment set,
// so installing this change does not move anybody's keyboard colour on its own.
//
// color_key is "accent" and not "auto" on purpose, and it is the whole point of
// `keyboard --theme`: the accent is the entry the now-playing integration
// overrides with the colour of the wallpaper, so "accent" is what makes the
// wallpaper decide. "auto" would take the hue farthest from the accent, which
// is the right answer for a light bar sitting next to the screen and the wrong
// one for the keys.
func DefaultKeyboardSettings() KeyboardSettings {
	return KeyboardSettings{
		Enabled:    true,
		Brightness: 8,
		ColorKey:   "accent",
	}
}

// Validate rejects values the controller cannot honour, at load time and with
// the file named, rather than later as a confusing HID error.
func (s KeyboardSettings) Validate() error {
	if s.Brightness < 0 || s.Brightness > keyboard.MaxBrightness {
		return fmt.Errorf("keyboard.brightness %d is outside 0-%d",
			s.Brightness, keyboard.MaxBrightness)
	}
	// The key is looked up in the theme's colors.toml, and a control character
	// or a line break could not have come from one.
	if err := printableOneLine(s.ColorKey); err != nil {
		return fmt.Errorf("keyboard.color_key: %w", err)
	}
	if s.ColorKey == "" {
		return fmt.Errorf("keyboard.color_key is empty; use \"accent\", \"auto\", " +
			"or any key from the theme's colors.toml")
	}
	return nil
}

// DefaultKeyboardSettingsSection is appended to the commented file written on
// first install. It is kept next to the struct so a new key cannot be added to
// one without the other being in view.
const DefaultKeyboardSettingsSection = `
[keyboard]
enabled = true
# 0-10, the same scale as ` + "`avellcc keyboard --brightness`" + `.
brightness = 8
# "accent" follows the theme's accent — and therefore the wallpaper, which is
# what the now-playing integration parks in the accent override. "auto" picks
# the palette hue farthest from the accent, and any other key from the theme's
# colors.toml also works, e.g. "bright_magenta".
color_key = "accent"
`
