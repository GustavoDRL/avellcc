package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/keyboard"
	"github.com/hugo-andrade/avellcc/internal/tui"
)

const (
	actionKeys   = "keys"
	actionLayout = "layout"
	actionOff    = "off"
	actionEffect = "effect"
)

var (
	kbColor      string
	kbKey        string
	kbEffect     string
	kbSpeed      int
	kbSpeedSet   bool
	kbBrightness int
	kbBrightSet  bool
	kbOff        bool
	kbProfile    string
	kbVerbose    bool
	kbStep       int
)

var keyboardCmd = &cobra.Command{
	Use:           "keyboard [keys|layout|calibrate|firmware]",
	Aliases:       []string{"kb"},
	Short:         "Control keyboard RGB LEDs",
	Args:          cobra.MaximumNArgs(1),
	RunE:          runKeyboard,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	f := keyboardCmd.Flags()
	f.StringVarP(&kbColor, "color", "c", "", "Set color (hex, name, or R,G,B)")
	f.StringVarP(&kbKey, "key", "k", "", "Target a specific key")

	allEffects := allEffectNames()
	f.StringVarP(&kbEffect, "effect", "e", "", fmt.Sprintf("Set effect (%s)", strings.Join(allEffects, ", ")))
	f.IntVarP(&kbSpeed, "speed", "s", 3, "Effect speed (0-10)")
	f.IntVarP(&kbBrightness, "brightness", "b", 0, "Set brightness (0-10)")
	f.BoolVar(&kbOff, "off", false, "Turn off keyboard LEDs")
	f.StringVarP(&kbProfile, "profile", "p", "", "Load a profile JSON file")
	f.BoolVarP(&kbVerbose, "verbose", "v", false, "Show grid positions (with keys action)")
	f.IntVar(&kbStep, "step", 1, "With calibrate: visit only every Nth column, as anchors to interpolate between")

	rootCmd.AddCommand(keyboardCmd)
}

// allEffectNames lists every effect the CLI accepts. Help text is built before
// a controller is detected, so it spans all supported controllers.
func allEffectNames() []string {
	names := make(map[string]bool)
	for _, k := range keyboard.AllHWEffectNames() {
		names[k] = true
	}
	for k := range keyboard.SoftwareEffects {
		names[k] = true
	}
	result := make([]string, 0, len(names))
	for k := range names {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

func runKeyboard(cmd *cobra.Command, args []string) error {
	kbSpeedSet = cmd.Flags().Changed("speed")
	kbBrightSet = cmd.Flags().Changed("brightness")

	// Determine action
	var action string
	if len(args) > 0 {
		action = args[0]
		switch action {
		case actionKeys, actionLayout, "calibrate", "firmware":
			// valid
		default:
			return fmt.Errorf("unknown action: %s (valid: keys, layout, calibrate, firmware)", action)
		}
	}

	// Validate args
	if err := validateKeyboardArgs(action); err != nil {
		return err
	}

	// Sub-actions
	switch action {
	case actionKeys:
		return kbKeys()
	case actionLayout:
		return kbLayout()
	case "calibrate":
		return kbCalibrate()
	case "firmware":
		return kbFirmware()
	}

	// LED control
	ctrl, err := keyboard.NewController()
	if err != nil {
		return err
	}
	if err := ctrl.Open(); err != nil {
		return err
	}
	defer func() { _ = ctrl.Close() }()

	// No flags → interactive TUI panel
	hasFlags := kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbBrightSet || kbOff || kbProfile != ""
	if !hasFlags {
		if _, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS); err != nil {
			return fmt.Errorf("interactive TUI requires a terminal; use flags for non-interactive mode")
		}
		panel := tui.NewKeyboardPanel(ctrl)
		p := tea.NewProgram(panel)
		_, err := p.Run()
		return err
	}

	bundle := config.LoadStateBundle()
	state := map[string]any{}

	if kbOff {
		if err := ctrl.Off(); err != nil {
			return err
		}
		state["mode"] = actionOff
		bundle["keyboard"] = state
		_ = config.SaveStateBundle(bundle)
		fmt.Println("Keyboard LEDs off.")
		return nil
	}

	applyBrightnessAfterProfile := kbBrightSet && kbProfile != ""

	// A profile may start a software effect, which only keeps rendering while
	// this process is alive.
	var profileRunner *keyboard.EffectRunner

	if kbBrightSet && !applyBrightnessAfterProfile {
		if err := ctrl.SetBrightness(kbBrightness); err != nil {
			return err
		}
		state["brightness"] = float64(kbBrightness)
		fmt.Printf("Brightness set to %d.\n", kbBrightness)
	}

	switch {
	case kbEffect != "":
		speed := kbSpeed
		if animID, ok := ctrl.HWEffects()[strings.ToLower(kbEffect)]; ok {
			if err := ctrl.SetHWAnimation(animID, speed); err != nil {
				return err
			}
			state["mode"] = actionEffect
			state[actionEffect] = kbEffect
			state["speed"] = float64(speed)
			fmt.Printf("Hardware effect '%s' activated.\n", kbEffect)
		} else {
			swName := strings.ToLower(kbEffect)
			if !strings.HasPrefix(swName, "sw_") {
				swName = "sw_" + swName
			}
			fn, ok := keyboard.SoftwareEffects[swName]
			if !ok {
				fn, ok = keyboard.SoftwareEffects[strings.ToLower(kbEffect)]
				if !ok {
					return fmt.Errorf("unknown effect '%s'; available: %s", kbEffect, strings.Join(allEffectNames(), ", "))
				}
			}
			runner := keyboard.NewEffectRunner(ctrl, 30)
			opts := keyboard.DefaultEffectOpts()
			opts.Speed = speed
			runner.Start(fn, opts)
			state["mode"] = actionEffect
			state[actionEffect] = kbEffect
			state["speed"] = float64(speed)
			bundle["keyboard"] = state
			_ = config.SaveStateBundle(bundle)
			fmt.Printf("Software effect '%s' running (speed=%d). Press Ctrl+C to stop.\n", kbEffect, speed)
			waitForEffect(runner)
			return nil
		}
	case kbColor != "":
		r, g, b, err := config.ParseColor(kbColor)
		if err != nil {
			return err
		}

		if kbKey != "" {
			keymap := keyboard.LoadKeymapFor(ctrl)
			pos, ok := keyboard.GetKeyPosition(kbKey, keymap)
			if !ok {
				if len(keymap) == 0 {
					return fmt.Errorf("no key map calibrated for the %s; run 'avellcc keyboard calibrate'", ctrl.Name())
				}
				return fmt.Errorf("unknown key: '%s'; use 'avellcc keyboard keys' to list keys", kbKey)
			}
			if err := ctrl.SetKeyColor(pos[0], pos[1], r, g, b); err != nil {
				return err
			}
			perKey, _ := state["per_key"].(map[string]any)
			if perKey == nil {
				perKey = map[string]any{}
			}
			perKey[strings.ToLower(kbKey)] = []any{float64(r), float64(g), float64(b)}
			state["per_key"] = perKey
			fmt.Printf("Key '%s' set to (%d, %d, %d).\n", kbKey, r, g, b)
		} else {
			if err := ctrl.SetAllKeys(r, g, b); err != nil {
				return err
			}
			state["mode"] = "static"
			state["color"] = []any{float64(r), float64(g), float64(b)}
			fmt.Printf("All keys set to (%d, %d, %d).\n", r, g, b)
		}
	case kbProfile != "":
		lbState, runner, err := loadProfile(ctrl, kbProfile)
		if err != nil {
			return err
		}
		profileRunner = runner
		state["mode"] = "profile"
		state["profile"] = kbProfile
		if lbState != nil {
			bundle["lightbar"] = lbState
		}
		fmt.Printf("Profile '%s' loaded.\n", kbProfile)
	}

	if applyBrightnessAfterProfile {
		if err := ctrl.SetBrightness(kbBrightness); err != nil {
			return err
		}
		state["brightness"] = float64(kbBrightness)
		fmt.Printf("Brightness set to %d.\n", kbBrightness)
	}

	if len(state) > 0 {
		bundle["keyboard"] = state
		_ = config.SaveStateBundle(bundle)
	}

	if profileRunner != nil {
		fmt.Println("Profile software effect running. Press Ctrl+C to stop.")
		waitForEffect(profileRunner)
	}

	return nil
}

// waitForEffect blocks until interrupted, keeping a software effect rendering.
// Returning would end the process and freeze the animation on its last frame.
func waitForEffect(runner *keyboard.EffectRunner) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	runner.Stop()
	fmt.Println("\nEffect stopped.")
}

func validateKeyboardArgs(action string) error {
	hasFlags := kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbBrightSet || kbOff || kbProfile != ""

	if action != "" {
		if action != actionKeys && kbVerbose {
			return fmt.Errorf("--verbose is only valid with 'keyboard keys'")
		}
		if hasFlags {
			return fmt.Errorf("'keyboard %s' does not accept LED control flags", action)
		}
		return nil
	}

	if kbVerbose {
		return fmt.Errorf("--verbose is only valid with 'keyboard keys'")
	}
	if kbKey != "" && kbColor == "" {
		return fmt.Errorf("--key requires --color")
	}
	if kbSpeedSet && kbEffect == "" {
		return fmt.Errorf("--speed requires --effect")
	}
	if kbEffect != "" && kbColor != "" {
		return fmt.Errorf("choose either --effect or --color")
	}
	if kbOff && (kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbBrightSet || kbProfile != "") {
		return fmt.Errorf("--off cannot be combined with other keyboard options")
	}
	if kbProfile != "" && (kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbOff) {
		return fmt.Errorf("--profile can only be combined with --brightness")
	}
	// No flags = launch interactive TUI (handled in runKeyboard)
	return nil
}

func kbKeys() error {
	// Key names are per-controller, so report the map that would actually be
	// used. Fall back to the ITE 8295 map when no device is attached.
	keymap := keyboard.DefaultMap8295
	if ctrl, err := keyboard.NewController(); err == nil {
		keymap = keyboard.LoadKeymapFor(ctrl)
		if len(keymap) == 0 {
			fmt.Printf("No key map calibrated for the %s yet.\n", ctrl.Name())
			fmt.Println("Run 'avellcc keyboard calibrate' to build one.")
			return nil
		}
	}
	keys := keyboard.ListKeys(keymap)
	if kbVerbose {
		for _, k := range keys {
			pos := keymap[k]
			fmt.Printf("  %-20s -> row=%d, col=%d\n", k, pos[0], pos[1])
		}
	} else {
		for i := 0; i < len(keys); i += 8 {
			end := i + 8
			if end > len(keys) {
				end = len(keys)
			}
			parts := make([]string, end-i)
			for j, k := range keys[i:end] {
				parts[j] = fmt.Sprintf("%-15s", k)
			}
			fmt.Println(strings.Join(parts, "  "))
		}
	}
	return nil
}

// kbLayout shows where each calibrated key sits on the LED grid, which is how
// a calibration gets checked without pressing every key again.
func kbLayout() error {
	ctrl, err := keyboard.NewController()
	if err != nil {
		return err
	}
	keymap := keyboard.LoadKeymapFor(ctrl)
	if len(keymap) == 0 {
		return fmt.Errorf("no key map calibrated for the %s; run 'avellcc keyboard calibrate'", ctrl.Name())
	}

	if _, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS); err != nil {
		fmt.Print(tui.RenderLayoutText(keymap, ctrl.Rows(), ctrl.Cols()))
		return nil
	}

	model := tui.NewKeyboardModel(keymap, savedKeyColors(keymap))
	_, err = tea.NewProgram(model).Run()
	return err
}

// savedKeyColors reconstructs what each mapped key should currently be showing,
// from the state avellcc last saved.
func savedKeyColors(keymap map[string][2]int) map[string][3]byte {
	colors := map[string][3]byte{}
	bundle := config.LoadStateBundle()
	kbState, ok := bundle["keyboard"].(map[string]any)
	if !ok {
		return colors
	}
	if rgb, ok := rgbFromAny(kbState["color"]); ok {
		for name := range keymap {
			colors[name] = rgb
		}
	}
	if perKey, ok := kbState["per_key"].(map[string]any); ok {
		for name, v := range perKey {
			if rgb, ok := rgbFromAny(v); ok {
				colors[keyboard.CanonicalKeyName(name)] = rgb
			}
		}
	}
	return colors
}

func rgbFromAny(v any) ([3]byte, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) != 3 {
		return [3]byte{}, false
	}
	var out [3]byte
	for i, c := range arr {
		n, ok := config.GetInt(map[string]any{"v": c}, "v")
		if !ok {
			return [3]byte{}, false
		}
		out[i] = byte(n)
	}
	return out, true
}

func kbCalibrate() error {
	ctrl, err := keyboard.NewController()
	if err != nil {
		return err
	}
	if err := ctrl.Open(); err != nil {
		return err
	}
	defer func() { _ = ctrl.Close() }()

	if _, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS); err != nil {
		return fmt.Errorf("calibration requires an interactive terminal")
	}

	panel := tui.NewCalibratePanel(ctrl, kbStep)
	if _, err := tea.NewProgram(panel).Run(); err != nil {
		return err
	}

	keymap := panel.Result()
	if len(keymap) == 0 {
		fmt.Println("Nothing mapped; key map left unchanged.")
	} else {
		if err := keyboard.SaveKeymapFor(ctrl, keymap); err != nil {
			return err
		}
		fmt.Printf("%s, saved to %s\n", panel.Summary(), keyboard.KeymapPathFor(ctrl))
	}

	// Calibration blanks the keyboard, so put the saved colours back.
	restoreKeyboardState(ctrl)
	return nil
}

// restoreKeyboardState re-applies whatever avellcc last saved, used after
// commands that take the keyboard over for their own display.
func restoreKeyboardState(ctrl keyboard.Controller) {
	bundle := config.LoadStateBundle()
	kbState, ok := bundle["keyboard"].(map[string]any)
	if !ok || len(kbState) == 0 {
		return
	}
	reloadKeyboardState(ctrl, kbState)
}

func kbFirmware() error {
	ctrl, err := keyboard.NewController()
	if err != nil {
		return err
	}
	if err := ctrl.Open(); err != nil {
		return err
	}
	defer func() { _ = ctrl.Close() }()

	data, err := ctrl.GetFirmwareInfo()
	if err != nil {
		return err
	}
	fmt.Printf("Controller: %s\n", ctrl.Name())
	fmt.Printf("Grid:       %d rows x %d cols\n", ctrl.Rows(), ctrl.Cols())
	fmt.Printf("Firmware:   %s\n", config.FormatHex(data))
	return nil
}

// loadProfile applies a profile. A software effect needs a live process to
// keep rendering, so the runner is handed back for the caller to own rather
// than started and abandoned here.
func loadProfile(ctrl keyboard.Controller, profilePath string) (map[string]any, *keyboard.EffectRunner, error) {
	profile, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, nil, err
	}

	var swRunner *keyboard.EffectRunner

	if brightness, ok := config.GetInt(profile, "brightness"); ok {
		_ = ctrl.SetBrightness(brightness)
	}

	if effect, ok := profile[actionEffect].(string); ok {
		speed := 3
		if s, ok := config.GetInt(profile, "speed"); ok {
			speed = s
		}
		if animID, ok := ctrl.HWEffects()[strings.ToLower(effect)]; ok {
			_ = ctrl.SetHWAnimation(animID, speed)
		} else {
			swName := strings.ToLower(effect)
			if !strings.HasPrefix(swName, "sw_") {
				swName = "sw_" + swName
			}
			if fn, ok := keyboard.SoftwareEffects[swName]; ok {
				swRunner = keyboard.NewEffectRunner(ctrl, 30)
				opts := keyboard.DefaultEffectOpts()
				opts.Speed = speed
				swRunner.Start(fn, opts)
			}
		}
	} else if colorVal, ok := profile["color"]; ok {
		var r, g, b byte
		switch c := colorVal.(type) {
		case string:
			r, g, b, err = config.ParseColor(c)
			if err != nil {
				return nil, nil, err
			}
		case []any:
			if len(c) == 3 {
				rv, _ := config.GetInt(map[string]any{"v": c[0]}, "v")
				gv, _ := config.GetInt(map[string]any{"v": c[1]}, "v")
				bv, _ := config.GetInt(map[string]any{"v": c[2]}, "v")
				r, g, b = byte(rv), byte(gv), byte(bv)
			}
		}
		_ = ctrl.SetAllKeys(r, g, b)
	}

	if keysMap, ok := profile[actionKeys].(map[string]any); ok {
		keymap := keyboard.LoadKeymapFor(ctrl)
		for keyName, colorVal := range keysMap {
			pos, found := keyboard.GetKeyPosition(keyName, keymap)
			if !found {
				continue
			}
			var r, g, b byte
			switch c := colorVal.(type) {
			case string:
				r, g, b, _ = config.ParseColor(c)
			case []any:
				if len(c) == 3 {
					rv, _ := config.GetInt(map[string]any{"v": c[0]}, "v")
					gv, _ := config.GetInt(map[string]any{"v": c[1]}, "v")
					bv, _ := config.GetInt(map[string]any{"v": c[2]}, "v")
					r, g, b = byte(rv), byte(gv), byte(bv)
				}
			}
			_ = ctrl.SetKeyColor(pos[0], pos[1], r, g, b)
		}
	}

	if lbRaw, ok := profile["lightbar"].(map[string]any); ok {
		mode, _ := lbRaw["mode"].(string)
		if mode == actionOff {
			appliedState := map[string]any{"mode": actionOff}
			_ = restoreLightbarState(appliedState, nil)
			return appliedState, swRunner, nil
		}

		var effectCode *byte
		var colorID *byte

		if effect, ok := lbRaw["effect"]; ok {
			ec, err := config.ParseLightbarEffect(fmt.Sprintf("%v", effect))
			if err == nil {
				effectCode = &ec
			}
		}
		if color, ok := lbRaw["color"]; ok {
			ci, err := config.ParseLightbarColor(fmt.Sprintf("%v", color))
			if err == nil {
				colorID = &ci
			}
		}

		updates := map[string]any{}
		if effectCode != nil {
			updates["effect_code"] = float64(*effectCode)
		}
		if colorID != nil {
			updates["color_id"] = float64(*colorID)
		}
		if br, ok := config.GetInt(lbRaw, "brightness"); ok {
			updates["brightness"] = float64(br)
		}
		if sp, ok := config.GetInt(lbRaw, "speed"); ok {
			updates["speed"] = float64(sp)
		}

		appliedState := config.MergeLightbarState(nil, updates)
		_ = restoreLightbarState(appliedState, nil)
		return appliedState, swRunner, nil
	}

	return nil, swRunner, nil
}
