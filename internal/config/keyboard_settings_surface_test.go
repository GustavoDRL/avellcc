package config

import (
	"reflect"
	"strings"
	"testing"
)

// keyboardTOMLKeys is the list of `keyboard.*` keys derived from the struct
// itself, so the tests below cannot go stale by being written down twice.
//
// The whole point is the field that does NOT exist yet: whoever adds a fourth
// key to KeyboardSettings tomorrow gets a red test naming the surface they
// forgot, instead of shipping a value that only exists inside lightbar.toml.
func keyboardTOMLKeys(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(KeyboardSettings{})
	keys := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("toml"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("%s has no toml tag; this test cannot name its key",
				typ.Field(i).Name)
		}
		keys = append(keys, "keyboard."+tag)
	}
	if len(keys) == 0 {
		t.Fatal("KeyboardSettings has no fields, which cannot be right")
	}
	return keys
}

// `avellcc lightbar config set keyboard.brightness 3` used to answer "unknown
// setting": settingFields() stopped at pulse.*, so the section the file
// documents was hand-edit only.
func TestEveryKeyboardKeyIsSettable(t *testing.T) {
	listed := make(map[string]bool)
	for _, key := range SettingKeys() {
		listed[key] = true
	}
	for _, key := range keyboardTOMLKeys(t) {
		if !listed[key] {
			t.Errorf("%s is in lightbar.toml but is not in SettingKeys(); "+
				"add it to settingFields() in lightbar_settings_set.go", key)
		}
	}
}

// Being listed is not the same as working: the entry has to parse, apply and
// round-trip through the file like every other key.
func TestKeyboardKeysRoundTripThroughTheFile(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	values := map[string]string{
		"keyboard.enabled":    "false",
		"keyboard.brightness": "3",
		"keyboard.color_key":  "bright_magenta",
	}
	// Guards against a new field being added to the struct and to
	// settingFields() while this test keeps exercising only the old three.
	if len(values) != len(keyboardTOMLKeys(t)) {
		t.Fatalf("this test covers %d keyboard keys but the struct has %d: %v",
			len(values), len(keyboardTOMLKeys(t)), keyboardTOMLKeys(t))
	}

	for key, want := range values {
		if err := WriteLightbarSetting(key, want); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatalf("the file no longer loads after writing the keyboard keys: %v", err)
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
	// The write must land in [keyboard] and not in whichever section the
	// scanner happened to be in — the audit found that failing before.
	if settings.Theme.Brightness != DefaultLightbarSettings().Theme.Brightness {
		t.Errorf("writing keyboard.brightness moved theme.brightness to %d",
			settings.Theme.Brightness)
	}
}

// A value the controller cannot honour must be refused by the setter too, not
// only by the loader — otherwise `config set` writes a file that then fails to
// load, which is the one state `config set` cannot repair.
func TestKeyboardSetterRejectsAnImpossibleBrightness(t *testing.T) {
	settings := DefaultLightbarSettings()
	if err := SetLightbarSetting(&settings, "keyboard.brightness", "99"); err == nil {
		t.Fatal("keyboard.brightness 99 was accepted")
	}
	if settings.Keyboard.Brightness != DefaultKeyboardSettings().Brightness {
		t.Errorf("the rejected value was left behind: brightness is %d",
			settings.Keyboard.Brightness)
	}
}
