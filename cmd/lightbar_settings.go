package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/omarchy"
	"github.com/hugo-andrade/avellcc/internal/pulse"
)

// effectiveLightbarSettings reads the settings file and lets flags win over
// it, so a value can be tried on the command line before being committed to
// the file. Only flags the user actually typed override; a flag left at its
// default must not silently outrank the file.
func effectiveLightbarSettings(cmd *cobra.Command) (config.LightbarSettings, error) {
	settings, err := config.LoadLightbarSettings()
	if err != nil {
		return settings, err
	}
	f := cmd.Flags()

	if f.Changed("theme-key") {
		settings.Theme.ColorKey = lbThemeKey
	}
	if f.Changed("brightness") {
		settings.Theme.Brightness = lbBrightness
	}
	if f.Changed("effect") {
		settings.Theme.Effect = config.NormalizeName(lbEffect)
	}
	if f.Changed("speed") {
		settings.Theme.Speed = lbSpeed
	}

	if f.Changed("pulse-fps") {
		settings.Pulse.FPS = pulseFPS
	}
	if f.Changed("pulse-min-brightness") {
		settings.Pulse.MinBrightness = pulseMinBrightness
	}
	if f.Changed("pulse-max-brightness") {
		settings.Pulse.MaxBrightness = pulseMaxBrightness
	}
	if f.Changed("pulse-gain") {
		settings.Pulse.Gain = pulseGain
	}
	if f.Changed("pulse-player") {
		settings.Pulse.Player = pulsePlayer
	}
	if f.Changed("pulse-input-method") {
		settings.Pulse.InputMethod = pulseInputMethod
	}
	if f.Changed("pulse-input-source") {
		settings.Pulse.InputSource = pulseInputSource
	}

	return settings, settings.Validate()
}

// pulseMapperConfig translates the file's pulse section into the mapper's
// tuning, leaving the smoothing constants — which have no reason to be
// user-facing — at their defaults.
func pulseMapperConfig(p config.PulseSettings) pulse.Config {
	cfg := pulse.DefaultConfig()
	cfg.MinBrightness = p.MinBrightness
	cfg.MaxBrightness = p.MaxBrightness
	cfg.Gain = p.Gain
	return cfg
}

// showLightbarConfig prints the settings actually in force and where each half
// came from. The file is optional, and "there is no file" is an answer worth
// printing rather than an absence to infer.
func showLightbarConfig(cmd *cobra.Command) error {
	settings, err := effectiveLightbarSettings(cmd)
	if err != nil {
		return err
	}

	path := config.LightbarSettingsPath()
	if _, statErr := os.Stat(path); statErr == nil {
		fmt.Printf("File: %s\n", path)
	} else {
		fmt.Printf("File: %s (absent — every value below is a default)\n", path)
	}

	source := func(flag string) string {
		if cmd.Flags().Changed(flag) {
			return "  (from --" + flag + ")"
		}
		return ""
	}

	fmt.Println("\n[theme]")
	fmt.Printf("  enabled        = %v\n", settings.Theme.Enabled)
	fmt.Printf("  brightness     = %d%s\n", settings.Theme.Brightness, source("brightness"))
	fmt.Printf("  effect         = %q%s\n", settings.Theme.Effect, source("effect"))
	fmt.Printf("  speed          = %d%s\n", settings.Theme.Speed, source("speed"))
	fmt.Printf("  color_key      = %q%s\n", settings.Theme.ColorKey, source("theme-key"))

	fmt.Println("\n[pulse]")
	fmt.Printf("  enabled        = %v\n", settings.Pulse.Enabled)
	fmt.Printf("  fps            = %d%s\n", settings.Pulse.FPS, source("pulse-fps"))
	fmt.Printf("  min_brightness = %d%s\n", settings.Pulse.MinBrightness, source("pulse-min-brightness"))
	fmt.Printf("  max_brightness = %d%s\n", settings.Pulse.MaxBrightness, source("pulse-max-brightness"))
	fmt.Printf("  gain           = %g%s\n", settings.Pulse.Gain, source("pulse-gain"))
	fmt.Printf("  player         = %q → %s%s\n", settings.Pulse.Player,
		settings.Pulse.MPRISName(), source("pulse-player"))
	fmt.Printf("  input_method   = %q%s\n", settings.Pulse.InputMethod, source("pulse-input-method"))
	fmt.Printf("  input_source   = %q%s\n", settings.Pulse.InputSource, source("pulse-input-source"))

	// [keyboard] rides in the same file (see internal/config/keyboard_settings.go)
	// and used to be invisible here: this function printed [theme] and [pulse]
	// and stopped, so the only way to discover the three keyboard values was to
	// open lightbar.toml. A tunable nobody can list is a tunable nobody finds,
	// which is the very argument that moved them out of the hook's environment
	// variables. No source() on these: `avellcc lightbar` has no keyboard flags
	// to override them with, so the file is always where they came from.
	fmt.Println("\n[keyboard]")
	fmt.Printf("  enabled        = %v\n", settings.Keyboard.Enabled)
	fmt.Printf("  brightness     = %d\n", settings.Keyboard.Brightness)
	fmt.Printf("  color_key      = %q\n", settings.Keyboard.ColorKey)

	// What the settings resolve to on the theme in force is the part that is
	// hard to predict from the file alone.
	if palette, err := omarchy.CurrentPalette(); err == nil {
		fmt.Printf("\nCurrent theme resolves to:\n")
		fmt.Printf("  rest / bass    %s\n", palette.Bass.Hex())
		fmt.Printf("  mid            %s   ← what the theme hook writes\n", palette.Mid.Hex())
		fmt.Printf("  treble         %s\n", palette.Treble.Hex())
	} else {
		fmt.Printf("\nCurrent theme could not be read: %v\n", err)
	}

	// The summary line has to name every section that has an `enabled`, or a
	// section left out of it reads as "on" to anyone who trusts the summary
	// over the fields above.
	var off []string
	if !settings.Theme.Enabled {
		off = append(off, "theme")
	}
	if !settings.Pulse.Enabled {
		off = append(off, "pulse")
	}
	if !settings.Keyboard.Enabled {
		off = append(off, "keyboard")
	}
	if len(off) > 0 {
		fmt.Printf("\nDisabled: %s\n", strings.Join(off, ", "))
	}
	return nil
}
