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
	kbTheme      bool
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
	f.BoolVar(&kbTheme, "theme", false,
		"Take the colour and brightness from the current Omarchy theme and [keyboard] in lightbar.toml")
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

	// --theme is resolved before anything is opened: the theme-set hook calls
	// this on every switch, and a theme that cannot be read must fail before
	// the keyboard has been half-written.
	if kbTheme {
		enabled, err := applyKeyboardThemeFlags()
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
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
	hasFlags := kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbBrightSet || kbOff || kbProfile != "" || kbTheme
	if !hasFlags {
		if _, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS); err != nil {
			return fmt.Errorf("interactive TUI requires a terminal; use flags for non-interactive mode")
		}
		panel := tui.NewKeyboardPanel(ctrl)
		p := tea.NewProgram(panel)
		_, err := p.Run()
		return err
	}

	// Every keyboard command updates the saved state, it does not replace it:
	// `--color` used to wipe the brightness the panel had just written, and each
	// `--key` used to wipe the keys set before it. What each command does
	// invalidate is spelled out in the state* helpers below, one case each.
	//
	// The changes are collected here and applied in a single locked
	// load-modify-save, so the copy they edit is the one that is about to be
	// written — not one read before a device write that took milliseconds.
	var stateUpdates []func(map[string]any)
	var lightbarState map[string]any

	if kbOff {
		if err := ctrl.Off(); err != nil {
			return err
		}
		_ = saveKeyboardState([]func(map[string]any){stateOff()}, nil)
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
		stateUpdates = append(stateUpdates, stateSetBrightness(kbBrightness))
		fmt.Printf("Brightness set to %d.\n", kbBrightness)
	}

	switch {
	case kbEffect != "":
		speed := kbSpeed
		if animID, ok := ctrl.HWEffects()[strings.ToLower(kbEffect)]; ok {
			if err := ctrl.SetHWAnimation(animID, speed); err != nil {
				return err
			}
			stateUpdates = append(stateUpdates, stateSetEffect(kbEffect, speed))
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
			stateUpdates = append(stateUpdates, stateSetEffect(kbEffect, speed))
			_ = saveKeyboardState(stateUpdates, nil)
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
			// On the ITE 8291 a per-key write cannot happen without entering
			// user mode (SET_EFFECT 51), and entering user mode is exactly what
			// takes a running hardware animation away — docs/ite8291-protocol.md,
			// "Per-key colour", and internal/keyboard/ite8291.go:enableUserMode,
			// which the write below goes through. So a saved mode=effect stops
			// being true the moment this lands.
			//
			// The ITE 8295's per-key command has no documented user mode, and
			// there is no such keyboard here to measure it on, so its state is
			// left exactly as it was rather than guessed at.
			_, perKeyEndsTheEffect := ctrl.(*keyboard.ITE8291)
			stateUpdates = append(stateUpdates,
				stateSetKeyColor(kbKey, r, g, b, perKeyEndsTheEffect))
			fmt.Printf("Key '%s' set to (%d, %d, %d).\n", kbKey, r, g, b)
		} else {
			if err := ctrl.SetAllKeys(r, g, b); err != nil {
				return err
			}
			stateUpdates = append(stateUpdates, stateSetColorAll(r, g, b))
			fmt.Printf("All keys set to (%d, %d, %d).\n", r, g, b)
		}
	case kbProfile != "":
		lbState, runner, err := loadProfile(ctrl, kbProfile)
		if err != nil {
			return err
		}
		profileRunner = runner
		stateUpdates = append(stateUpdates, stateSetProfile(kbProfile, kbBrightSet))
		lightbarState = lbState
		fmt.Printf("Profile '%s' loaded.\n", kbProfile)
	}

	if applyBrightnessAfterProfile {
		if err := ctrl.SetBrightness(kbBrightness); err != nil {
			return err
		}
		stateUpdates = append(stateUpdates, stateSetBrightness(kbBrightness))
		fmt.Printf("Brightness set to %d.\n", kbBrightness)
	}

	_ = saveKeyboardState(stateUpdates, lightbarState)

	if profileRunner != nil {
		fmt.Println("Profile software effect running. Press Ctrl+C to stop.")
		waitForEffect(profileRunner)
	}

	return nil
}

// applyKeyboardThemeFlags fills in --color and --brightness from the applied
// Omarchy theme and the [keyboard] section of lightbar.toml, and reports
// whether the keyboard half is enabled at all.
//
// The owner's rule is that the wallpaper decides the keyboard's colour. The
// wallpaper reaches avellcc as the accent override, so this resolves through
// themeColor — the same function `lightbar --theme` uses — with
// keyboard.color_key, which defaults to "accent" for exactly that reason.
// The hook used to read colors.toml with sed and write the theme's own accent,
// which the now-playing integration then overwrote a second or three later:
// measured at #FFB6D1 for three seconds, then #8AA4B0. This makes the first
// write already be the right colour.
//
// It reads the file directly rather than through effectiveLightbarSettings:
// that overlay reads the *lightbar* command's flag variables, where
// --brightness is 0-100, and the keyboard's is 0-10. Sharing it would let one
// command's flag land on the other's scale.
func applyKeyboardThemeFlags() (bool, error) {
	settings, err := config.LoadLightbarSettings()
	if err != nil {
		return false, err
	}
	// Disabling the keyboard half has to be a silent success, exactly as it is
	// for the light bar: the hook runs on every theme switch and must not start
	// failing because the user turned this off.
	if !settings.Keyboard.Enabled {
		return false, nil
	}
	color, err := themeColor(settings.Keyboard.ColorKey)
	if err != nil {
		return false, err
	}
	kbColor = color
	// The settings file stands in for a --brightness nobody typed, so a theme
	// switch reproduces the file rather than whatever was last set by hand.
	if !kbBrightSet {
		kbBrightness, kbBrightSet = settings.Keyboard.Brightness, true
	}
	return true, nil
}

// saveKeyboardState applies the collected changes to the saved keyboard state.
//
// The load happens inside the lock and inside the same call as the save, which
// is what makes `--color` keep the brightness and each `--key` keep the keys
// set before it. lightbarState, when not nil, is written in the same update: a
// profile carries both halves and they have to land together.
func saveKeyboardState(updates []func(map[string]any), lightbarState map[string]any) error {
	if len(updates) == 0 && lightbarState == nil {
		return nil
	}
	return config.UpdateStateBundle(func(bundle map[string]any) error {
		state, _ := bundle["keyboard"].(map[string]any)
		if state == nil {
			state = map[string]any{}
		}
		for _, update := range updates {
			update(state)
		}
		bundle["keyboard"] = state
		if lightbarState != nil {
			bundle["lightbar"] = lightbarState
		}
		return nil
	})
}

// The state* helpers below each describe one command: what it records, and
// what it deliberately drops. Dropping used to happen by accident — the whole
// keyboard state was replaced — which is how a per-key colour survived an
// effect that had already painted over it.

// stateSetBrightness records the brightness and nothing else. Brightness is
// orthogonal to what is on the keys, and reload applies it either way.
// stateSetBrightness also clears a leftover "off": after `--brightness 20` the
// keyboard is lit, not off. Keeping mode=off would make the saved state say two
// contradictory things, and reload resolves that contradiction the wrong way --
// it calls ctrl.Off() and only then SetBrightness, lighting the keyboard back
// up. That is exactly what the stateOff comment below warns about, so the state
// must not be allowed to reach that shape in the first place.
func stateSetBrightness(brightness int) func(map[string]any) {
	return func(state map[string]any) {
		state["brightness"] = float64(brightness)
		if mode, _ := state["mode"].(string); mode == actionOff {
			delete(state, "mode")
		}
	}
}

// stateSetEffect drops the colours: an animation owns every LED, so the static
// colour and the per-key colours are gone from the hardware. Left in the state,
// reload would repaint them over the running animation.
func stateSetEffect(effect string, speed int) func(map[string]any) {
	return func(state map[string]any) {
		state["mode"] = actionEffect
		state[actionEffect] = effect
		state["speed"] = float64(speed)
		delete(state, "color")
		delete(state, "per_key")
	}
}

// stateSetColorAll drops the effect and the per-key colours: SetAllKeys repaints
// the whole grid, which is exactly what cancels both.
func stateSetColorAll(r, g, b byte) func(map[string]any) {
	return func(state map[string]any) {
		state["mode"] = "static"
		state["color"] = []any{float64(r), float64(g), float64(b)}
		delete(state, actionEffect)
		delete(state, "speed")
		delete(state, "per_key")
	}
}

// stateSetKeyColor changes one key. The base colour, the brightness and the
// keys coloured before it are all still lit on the keyboard, so they are all
// still true of the saved state and all stay.
//
// A running hardware effect is the exception, and only where the driver says
// so: endsTheEffect comes from the call site, which knows whether this
// controller's per-key write cancels the animation. When it does, leaving
// mode=effect behind would make `avellcc reload` restart an animation the
// keyboard is no longer running — and that animation owns every LED, so it
// would paint straight over the key this command just set. mode=static with no
// "color" repaints nothing on reload, which is right: the rest of the grid is
// whatever the controller's framebuffer already holds.
func stateSetKeyColor(key string, r, g, b byte, endsTheEffect bool) func(map[string]any) {
	return func(state map[string]any) {
		if mode, _ := state["mode"].(string); endsTheEffect && mode == actionEffect {
			state["mode"] = "static"
			delete(state, actionEffect)
			delete(state, "speed")
		}
		perKey, _ := state["per_key"].(map[string]any)
		if perKey == nil {
			perKey = map[string]any{}
		}
		perKey[strings.ToLower(key)] = []any{float64(r), float64(g), float64(b)}
		state["per_key"] = perKey
	}
}

// stateSetProfile drops everything the profile now owns, brightness included —
// reload applies the state's brightness *after* loading the profile, so a
// leftover value would override the profile's own. It survives only when the
// user set it explicitly on the same command line.
func stateSetProfile(path string, brightnessGiven bool) func(map[string]any) {
	return func(state map[string]any) {
		state["mode"] = "profile"
		state["profile"] = path
		delete(state, "color")
		delete(state, actionEffect)
		delete(state, "speed")
		delete(state, "per_key")
		if !brightnessGiven {
			delete(state, "brightness")
		}
	}
}

// stateOff is the one command that does replace the whole state, and it has to:
// reload applies the brightness and the per-key colours after ctrl.Off(), so
// anything left behind here would light the keyboard straight back up.
func stateOff() func(map[string]any) {
	return func(state map[string]any) {
		for key := range state {
			delete(state, key)
		}
		state["mode"] = actionOff
	}
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
	hasFlags := kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbBrightSet || kbOff || kbProfile != "" || kbTheme

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
	// --theme *is* the colour, so anything that would also decide what the keys
	// show is a contradiction. --brightness stays allowed: it overrides the one
	// the settings file supplies, the same way it does on the light bar.
	if kbTheme && (kbColor != "" || kbKey != "" || kbEffect != "" || kbSpeedSet || kbOff || kbProfile != "") {
		return fmt.Errorf("--theme takes the colour from the current theme; " +
			"it combines only with --brightness")
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
	// Say what the hardware refused. Calibration has just blanked the keyboard,
	// so a silent failure here leaves it dark with nothing to explain why.
	if err := reloadKeyboardState(ctrl, kbState); err != nil {
		fmt.Printf("Keyboard: %v\n", err)
	}
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
