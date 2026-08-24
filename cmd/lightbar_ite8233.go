package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/hidraw"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

// The ITE 8233 chassis lightbar takes true RGB rather than the ITE 8911's
// palette of colour IDs, so it does not share the X58 state map or the X58
// interactive panel. Its state carries a controller marker to keep the two
// from being read into each other.
const lb8233StateKey = "ite8233"

// runLightbar8233 handles the whole `avellcc lightbar` surface when the
// machine has an ITE 8233 chassis bar instead of a Clevo rear lightbar.
func runLightbar8233(cmd *cobra.Command, path string, product uint16) error {
	brightSet := cmd.Flags().Changed("brightness")
	speedSet := cmd.Flags().Changed("speed")

	if lbOff && (lbEffect != "" || lbColor != "" || brightSet || speedSet) {
		return fmt.Errorf("--off cannot be combined with other lightbar options")
	}
	if lbEffectCode != "" || cmd.Flags().Changed("color-id") {
		return fmt.Errorf("--effect-code and --color-id are ITE 8911 options; " +
			"this machine has an ITE 8233, which takes --effect and an RGB --color")
	}

	if pulseEnable && (lbEffect != "" || lbColor != "" || brightSet || speedSet || lbOff || lbTheme || lbShowConfig) {
		return fmt.Errorf("--pulse runs the bar continuously and takes no other lightbar " +
			"options; it reads the theme itself")
	}

	if lbShowConfig {
		return showLightbarConfig(cmd)
	}

	if lbTheme {
		if lbColor != "" {
			return fmt.Errorf("--theme takes the colour from the theme; drop --color")
		}
		settings, err := effectiveLightbarSettings(cmd)
		if err != nil {
			return err
		}
		// Disabling the theme half in the settings file has to be a silent
		// success: the hook calls this on every theme switch and must not
		// start failing because the user turned the light bar off.
		if !settings.Theme.Enabled {
			return nil
		}
		if lbColor, err = themeColor(settings.Theme.ColorKey); err != nil {
			return err
		}
		// The settings file stands in for the saved state on this path, so a
		// theme switch reproduces the file rather than whatever was last set
		// by hand.
		if !brightSet {
			lbBrightness, brightSet = settings.Theme.Brightness, true
		}
		if lbEffect == "" {
			lbEffect = settings.Theme.Effect
		}
		if !speedSet {
			lbSpeed, speedSet = settings.Theme.Speed, true
		}
	}

	ctrl := lightbar.NewITE8233(&hidraw.HidrawDevice{Path: path}, product)
	if err := ctrl.Open(); err != nil {
		return err
	}
	defer func() { _ = ctrl.Close() }()

	// The pulse daemon owns the bar for as long as it runs, so it never shares
	// an invocation with a one-shot write.
	if pulseEnable {
		return runLightbarPulse(cmd, ctrl)
	}

	if lbDebugDescriptor {
		desc, err := hidraw.ReportDescriptor(path)
		if err != nil {
			return err
		}
		fmt.Printf("Descriptor (%d bytes): %s\n", len(desc), config.FormatHex(desc))
		return nil
	}

	if lbDebugRaw != "" {
		payload, err := config.ParseBytes(lbDebugRaw)
		if err != nil {
			return err
		}
		if len(payload) != 8 {
			return fmt.Errorf("the ITE 8233 control packet is exactly 8 bytes, got %d", len(payload))
		}
		if err := ctrl.SendRaw([8]byte(payload)); err != nil {
			return err
		}
		fmt.Printf("Sent: %s\n", config.FormatHex(payload))
		return nil
	}

	state := loadLightbar8233State()

	if lbOff {
		if err := ctrl.Off(); err != nil {
			return err
		}
		_ = updateLightbar8233State(map[string]any{"mode": actionOff})
		fmt.Println("Lightbar off.")
		return nil
	}

	if lbEffect == "" && lbColor == "" && !brightSet && !speedSet {
		lightbar8233Status(ctrl, state)
		return nil
	}

	// Unset flags fall back to the stored state, so changing one setting does
	// not silently reset the others.
	effect, hasEffect := config.GetString(state, "effect")
	if !hasEffect || effect == "" {
		effect = "static"
	}
	if lbEffect != "" {
		effect = config.NormalizeName(lbEffect)
	}
	if _, ok := lightbar.Effects8233[effect]; !ok {
		return fmt.Errorf("unknown lightbar effect %q (have %s)",
			effect, strings.Join(lightbar.EffectNames8233(), ", "))
	}

	color, err := lightbar8233Color(state)
	if err != nil {
		return err
	}
	if lbColor != "" {
		if color, err = parseRGB8233(lbColor); err != nil {
			return err
		}
	}

	brightness, hasBrightness := config.GetInt(state, "brightness")
	if !hasBrightness {
		brightness = lightbar.MaxBrightness8233
	}
	if brightSet {
		brightness = lbBrightness
	}

	speed, hasSpeed := config.GetInt(state, "speed")
	if !hasSpeed {
		speed = 5
	}
	if speedSet {
		speed = lbSpeed
	}

	if effect == "static" {
		err = ctrl.SetColor(color[0], color[1], color[2], brightness)
	} else {
		// The animated modes walk a seven-slot list. A colour given on the
		// command line paints every slot, which turns wave or bounce into a
		// single-hue sweep; without one they cycle the factory rainbow.
		colors := lightbar.Rainbow8233
		if lbColor != "" {
			for i := range colors {
				colors[i] = color
			}
		}
		err = ctrl.SetEffect(effect, colors, brightness, speed)
	}
	if err != nil {
		return err
	}

	hex := fmt.Sprintf("#%02x%02x%02x", color[0], color[1], color[2])
	_ = updateLightbar8233State(map[string]any{
		"mode":       "active",
		"effect":     effect,
		"color":      hex,
		"brightness": float64(brightness),
		"speed":      float64(speed),
		// Whether the animation ran on one colour or on the rainbow is not
		// recoverable from the colour alone, and reload has to reproduce it.
		"solid_color": lbColor != "",
	})

	fmt.Printf("Lightbar updated: effect=%s, color=%s, brightness=%d, speed=%d.\n",
		effect, hex, brightness, speed)
	return nil
}

// updateLightbar8233State writes the given fields into the saved chassis-bar
// state inside the state lock, keeping the controller marker and the fields
// this command did not touch.
//
// The read that decides what to *send* still happens before the device write —
// it has to, since an unset flag falls back to the stored value — but re-reading
// here means a state written meanwhile is not rolled back to what the file said
// before the HID transfer, and the HID transfer is the slow part. The other
// writers of this file are the theme hook, the now-playing hook, the pulse
// daemon restoring the bar, and `avellcc reload`.
func updateLightbar8233State(fields map[string]any) error {
	return config.UpdateStateBundle(func(bundle map[string]any) error {
		state, _ := bundle["lightbar"].(map[string]any)
		// State left behind by the ITE 8911 driver is a different shape and
		// must not be merged into this one — the same rule, and the same
		// marker, as loadLightbar8233State.
		if controller, _ := config.GetString(state, "controller"); controller != lb8233StateKey {
			state = map[string]any{}
		}
		state["controller"] = lb8233StateKey
		for k, v := range fields {
			state[k] = v
		}
		bundle["lightbar"] = state
		return nil
	})
}

// restoreLightbar8233State reapplies the saved chassis-bar state. The boot
// service and the suspend/resume hook both go through here. Whether this MCU
// keeps its state across suspend is unmeasured; tuxedo-drivers rewrites the
// colour on every resume, which suggests it does not, and two HID packets is a
// cheap way to not depend on the answer.
func restoreLightbar8233State(state map[string]any) error {
	path, product, err := lightbar.FindITE8233()
	if err != nil {
		return err
	}
	ctrl := lightbar.NewITE8233(&hidraw.HidrawDevice{Path: path}, product)
	if err := ctrl.Open(); err != nil {
		return err
	}
	defer func() { _ = ctrl.Close() }()

	if mode, _ := config.GetString(state, "mode"); mode == actionOff {
		return ctrl.Off()
	}

	color, err := lightbar8233Color(state)
	if err != nil {
		return err
	}
	brightness, ok := config.GetInt(state, "brightness")
	if !ok {
		brightness = lightbar.MaxBrightness8233
	}
	speed, ok := config.GetInt(state, "speed")
	if !ok {
		speed = 5
	}

	effect, _ := config.GetString(state, "effect")
	if effect == "" || effect == "static" {
		return ctrl.SetColor(color[0], color[1], color[2], brightness)
	}
	colors := lightbar.Rainbow8233
	if solid, _ := state["solid_color"].(bool); solid {
		for i := range colors {
			colors[i] = color
		}
	}
	return ctrl.SetEffect(effect, colors, brightness, speed)
}

// loadLightbar8233State reads this controller's saved state, discarding any
// state left behind by the ITE 8911 driver.
func loadLightbar8233State() map[string]any {
	bundle := config.LoadStateBundle()
	state, _ := bundle["lightbar"].(map[string]any)
	if controller, _ := config.GetString(state, "controller"); controller != lb8233StateKey {
		state = map[string]any{}
	}
	state["controller"] = lb8233StateKey
	return state
}

func lightbar8233Color(state map[string]any) ([3]byte, error) {
	if saved, ok := config.GetString(state, "color"); ok {
		return parseRGB8233(saved)
	}
	return [3]byte{0xFF, 0xFF, 0xFF}, nil
}

// parseRGB8233 accepts #rrggbb, rrggbb, or one of the named colours the ITE
// 8911 palette already defines, so the same names work on both controllers.
func parseRGB8233(value string) ([3]byte, error) {
	s := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(value)), "#")
	if id, err := config.ParseLightbarColor(value); err == nil {
		if hex, ok := lightbar.X58ColorHex[id]; ok {
			s = strings.TrimPrefix(hex, "#")
		}
	}
	if len(s) != 6 {
		return [3]byte{}, fmt.Errorf("colour %q is not #rrggbb or a known colour name", value)
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return [3]byte{}, fmt.Errorf("colour %q is not #rrggbb or a known colour name", value)
	}
	return [3]byte{byte(n >> 16), byte(n >> 8), byte(n)}, nil
}

func lightbar8233Status(ctrl *lightbar.ITE8233, state map[string]any) {
	fmt.Printf("Device: %s (%s, %04x:%04x)\n", ctrl.Path(), ctrl.Name(), lightbar.VID8233, ctrl.Product())

	// The controller has no readable state: its GET_FEATURE answers all zeros
	// whatever the bar is doing, so what follows is what avellcc last wrote.
	if mode, _ := config.GetString(state, "mode"); mode == actionOff {
		fmt.Println("State: off (last set by avellcc)")
	} else if effect, ok := config.GetString(state, "effect"); ok {
		color, _ := config.GetString(state, "color")
		br, _ := config.GetInt(state, "brightness")
		sp, _ := config.GetInt(state, "speed")
		fmt.Printf("State: effect=%s, color=%s, brightness=%d, speed=%d (last set by avellcc)\n",
			effect, color, br, sp)
	} else {
		fmt.Println("State: unknown — the controller does not report it and avellcc has not written it")
	}

	fmt.Printf("\nEffects:    %s\n", strings.Join(lightbar.EffectNames8233(), ", "))
	fmt.Printf("Color:      #rrggbb, or %s\n", strings.Join(sortedKeys(lightbar.X58ColorIDs), ", "))
	fmt.Printf("Brightness: 0-%d\n", lightbar.MaxBrightness8233)
	fmt.Printf("Speed:      %d-%d (fastest first)\n", lightbar.MinSpeed8233, lightbar.MaxSpeed8233)
}
