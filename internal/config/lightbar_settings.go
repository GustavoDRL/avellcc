package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

// Settings for the chassis light bar, read from one file that both the
// theme-set hook and the pulse daemon consult.
//
// There used to be two places: AVELLCC_THEME_LIGHTBAR_* environment variables
// for the hook, and command-line flags baked into the systemd unit for the
// daemon. Two syntaxes for one feature, neither discoverable from the other.
// The environment variables are gone; this file replaces them.
//
// Keys mirror the flag names — `pulse.fps` is `--pulse-fps` — so `--help`
// documents the file, and a flag still wins over the file for one-off tuning.
type LightbarSettings struct {
	Theme ThemeSettings `toml:"theme" json:"theme"`
	Pulse PulseSettings `toml:"pulse" json:"pulse"`
}

// ThemeSettings is what the bar shows at rest: the colour the theme-set hook
// writes after every `omarchy theme set`.
type ThemeSettings struct {
	Enabled    bool   `toml:"enabled" json:"enabled"`
	Brightness int    `toml:"brightness" json:"brightness"`
	Effect     string `toml:"effect" json:"effect"`
	Speed      int    `toml:"speed" json:"speed"`
	ColorKey   string `toml:"color_key" json:"color_key"`
}

// PulseSettings is what the bar does while music plays.
type PulseSettings struct {
	Enabled       bool    `toml:"enabled" json:"enabled"`
	FPS           int     `toml:"fps" json:"fps"`
	MinBrightness int     `toml:"min_brightness" json:"min_brightness"`
	MaxBrightness int     `toml:"max_brightness" json:"max_brightness"`
	Gain          float64 `toml:"gain" json:"gain"`
	Player        string  `toml:"player" json:"player"`
	InputMethod   string  `toml:"input_method" json:"input_method"`
	InputSource   string  `toml:"input_source" json:"input_source"`
}

// DefaultLightbarSettings matches what the code did before the file existed,
// so writing no file and writing the defaults are the same thing.
func DefaultLightbarSettings() LightbarSettings {
	return LightbarSettings{
		Theme: ThemeSettings{
			Enabled:    true,
			Brightness: 80,
			Effect:     "static",
			Speed:      5,
			ColorKey:   "auto",
		},
		Pulse: PulseSettings{
			Enabled:       true,
			FPS:           30,
			MinBrightness: 12,
			MaxBrightness: 100,
			Gain:          2.0,
			Player:        "spotify",
			InputMethod:   "pipewire",
			InputSource:   "auto",
		},
	}
}

// LightbarSettingsPath is the one file.
func LightbarSettingsPath() string {
	return filepath.Join(ConfigDir(), "lightbar.toml")
}

// MPRISName expands a short player name to its bus name. Writing
// `player = "spotify"` should not require knowing the D-Bus naming convention,
// but a full bus name has to keep working for players that do not follow it.
func (p PulseSettings) MPRISName() string {
	if strings.Contains(p.Player, ".") {
		return p.Player
	}
	return "org.mpris.MediaPlayer2." + p.Player
}

// LoadLightbarSettings reads the file over the defaults. A missing file is not
// an error: it means every default applies.
func LoadLightbarSettings() (LightbarSettings, error) {
	data, err := os.ReadFile(LightbarSettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultLightbarSettings(), nil
		}
		return DefaultLightbarSettings(), err
	}
	settings, err := DecodeLightbarSettings(string(data))
	if err != nil {
		return DefaultLightbarSettings(), fmt.Errorf("%s: %w", LightbarSettingsPath(), err)
	}
	return settings, nil
}

// DecodeLightbarSettings parses a settings file body over the defaults and
// validates the result. It is separate from LoadLightbarSettings so the writer
// can check a candidate file before it reaches the disk — see
// WriteLightbarSetting, where that check is what makes an in-place edit safe.
func DecodeLightbarSettings(body string) (LightbarSettings, error) {
	settings := DefaultLightbarSettings()

	meta, err := toml.Decode(body, &settings)
	if err != nil {
		return DefaultLightbarSettings(), err
	}
	// A misspelled key would otherwise be silently ignored, and the user would
	// be left staring at a setting that visibly does nothing.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return DefaultLightbarSettings(), fmt.Errorf("unknown setting %s", strings.Join(keys, ", "))
	}

	return settings, settings.Validate()
}

// Validate rejects values the hardware or cava cannot honour. It runs on load
// rather than at the point of use so a typo is reported once, with the file
// named, instead of surfacing later as a confusing HID error.
func (s LightbarSettings) Validate() error {
	if _, ok := lightbar.Effects8233[s.Theme.Effect]; !ok {
		return fmt.Errorf("theme.effect %q is not one of %s",
			s.Theme.Effect, strings.Join(lightbar.EffectNames8233(), ", "))
	}
	if s.Theme.Brightness < 0 || s.Theme.Brightness > lightbar.MaxBrightness8233 {
		return fmt.Errorf("theme.brightness %d is outside 0-%d",
			s.Theme.Brightness, lightbar.MaxBrightness8233)
	}
	if s.Theme.Speed < lightbar.MinSpeed8233 || s.Theme.Speed > lightbar.MaxSpeed8233 {
		return fmt.Errorf("theme.speed %d is outside %d-%d",
			s.Theme.Speed, lightbar.MinSpeed8233, lightbar.MaxSpeed8233)
	}
	if s.Pulse.FPS < 1 || s.Pulse.FPS > 120 {
		return fmt.Errorf("pulse.fps %d is outside 1-120", s.Pulse.FPS)
	}
	if s.Pulse.MinBrightness < 0 || s.Pulse.MaxBrightness > lightbar.MaxBrightness8233 {
		return fmt.Errorf("pulse brightness range %d-%d is outside 0-%d",
			s.Pulse.MinBrightness, s.Pulse.MaxBrightness, lightbar.MaxBrightness8233)
	}
	if s.Pulse.MinBrightness > s.Pulse.MaxBrightness {
		return fmt.Errorf("pulse.min_brightness %d is above pulse.max_brightness %d",
			s.Pulse.MinBrightness, s.Pulse.MaxBrightness)
	}
	// `<= 0` is false for NaN and for +Inf, both of which ParseFloat accepts
	// and neither of which is valid TOML — so the write would succeed and the
	// file would then be unloadable.
	if math.IsNaN(s.Pulse.Gain) || math.IsInf(s.Pulse.Gain, 0) {
		return fmt.Errorf("pulse.gain must be a finite number")
	}
	if s.Pulse.Gain <= 0 || s.Pulse.Gain > 10 {
		return fmt.Errorf("pulse.gain %g is outside 0-10 (exclusive of 0)", s.Pulse.Gain)
	}
	if err := validBusSegment(s.Pulse.Player); err != nil {
		return fmt.Errorf("pulse.player: %w", err)
	}
	// cava exits with an error on an unknown method, and the daemon would then
	// respawn it every two seconds forever with no clear diagnosis.
	if !slices.Contains(CavaInputMethods, s.Pulse.InputMethod) {
		return fmt.Errorf("pulse.input_method %q is not one of %s",
			s.Pulse.InputMethod, strings.Join(CavaInputMethods, ", "))
	}
	// Both strings are interpolated into the generated cava config, so a
	// newline would inject arbitrary directives into it.
	for name, value := range map[string]string{
		"pulse.input_method": s.Pulse.InputMethod,
		"pulse.input_source": s.Pulse.InputSource,
		"pulse.player":       s.Pulse.Player,
		"theme.color_key":    s.Theme.ColorKey,
	} {
		if err := printableOneLine(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// CavaInputMethods is cava's own list, as it prints when given an unknown one.
var CavaInputMethods = []string{
	"fifo", "portaudio", "pipewire", "alsa", "pulse", "sndio", "jack", "shmem",
}

// printableOneLine rejects values that cannot survive a round trip through
// TOML and through the generated cava config.
func printableOneLine(v string) error {
	for _, r := range v {
		if r == '\n' || r == '\r' {
			return fmt.Errorf("must not contain a line break")
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

// validBusSegment keeps pulse.player to what D-Bus accepts in a match rule; a
// space makes dbus-monitor reject the rule outright and playback is then never
// detected.
func validBusSegment(name string) error {
	if name == "" {
		return fmt.Errorf("is empty")
	}
	for _, r := range name {
		ok := r == '.' || r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("%q is not a valid D-Bus name (letters, digits, dot, dash, underscore)", name)
		}
	}
	return nil
}

// DefaultLightbarSettingsFile is the commented file written on first install.
// It is written once and never rewritten, so a user's edits and comments are
// never clobbered by an upgrade.
const DefaultLightbarSettingsFile = `# Chassis light bar — one file for both halves of the integration.
#
# [theme] is what the bar shows at rest, written after every ` + "`omarchy theme set`" + `.
# [pulse] is what it does while music plays.
#
# Every key mirrors a flag on ` + "`avellcc lightbar`" + `, and a flag wins over this file,
# so you can try a value before committing to it:
#
#   avellcc lightbar --pulse --pulse-fps 60 --pulse-debug
#
# The pulse daemon re-reads this file about once a second. Changing brightness
# or gain takes effect immediately; changing fps or the cava input restarts the
# capture; changing the player needs ` + "`systemctl --user restart avellcc-pulse`" + `.
#
# See the effective values, and where each came from:
#
#   avellcc lightbar --show-config

[theme]
enabled = true
# 0-100.
brightness = 80
# static, breathing, wave, bounce, marquee, scan.
effect = "static"
# 1 (fastest) to 10. Only animated effects use it.
speed = 5
# "auto" picks the palette hue farthest from the theme's accent. Any key from
# the theme's colors.toml also works, e.g. "bright_magenta" or "foreground".
color_key = "auto"

[pulse]
enabled = true
# Frame rate, and cava's. Measured clean to 60 on this controller.
fps = 30
# Brightness between beats. Not 0: a bar that goes fully dark reads as broken.
min_brightness = 12
max_brightness = 100
# Corrects for cava normalising the loudest bar rather than the average of
# them, which otherwise leaves max_brightness unreachable. Raise if the bar
# still never reaches the ceiling on loud passages.
gain = 2.0
# Short name, or a full MPRIS bus name for a player that does not follow the
# org.mpris.MediaPlayer2.<name> convention. A player that qualifies its bus
# name with an instance suffix — "OmarchySpotify" publishes
# org.mpris.MediaPlayer2.OmarchySpotify.instance<pid> — is matched by the name
# without the suffix, so the short name is still what goes here.
player = "spotify"
# cava's capture. "auto" follows the default sink's monitor.
input_method = "pipewire"
input_source = "auto"
`

// WriteDefaultLightbarSettingsFile creates the commented file if it is absent,
// and reports whether it wrote one.
func WriteDefaultLightbarSettingsFile() (bool, error) {
	unlock, err := lockSettings()
	if err != nil {
		return false, err
	}
	defer unlock()
	return writeDefaultSettingsFileLocked()
}

// writeDefaultSettingsFileLocked is the same thing for a caller that already
// holds the lock. Splitting it keeps the lock non-reentrant, which flock is.
func writeDefaultSettingsFileLocked() (bool, error) {
	path := LightbarSettingsPath()
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, atomicWriteFile(path, []byte(DefaultLightbarSettingsFile))
}
