package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/keyboard"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

var reloadCmd = &cobra.Command{
	Use:           "reload",
	Short:         "Reload saved keyboard and lightbar state",
	Args:          cobra.NoArgs,
	RunE:          runReload,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}

func runReload(cmd *cobra.Command, args []string) error {
	bundle := config.LoadStateBundle()
	if len(bundle) == 0 {
		fmt.Println("No saved state found.")
		return nil
	}

	reloaded := false
	// A device that is not there is not a failure: this command runs from the
	// boot service and from the resume hook, on machines that may have neither
	// controller. A device that IS there and rejected every write is a failure,
	// and it used to be indistinguishable from success — same message, same
	// exit 0 — which is what let a keyboard come back blank after a resume with
	// nothing in the journal to say so.
	var failed error

	// Keyboard
	if kbState, ok := bundle["keyboard"].(map[string]any); ok && len(kbState) > 0 {
		ctrl, err := keyboard.NewController()
		if err != nil {
			fmt.Printf("Keyboard: %v, skipping.\n", err)
		} else if err := ctrl.Open(); err != nil {
			fmt.Printf("Keyboard: %v, skipping.\n", err)
		} else {
			err := reloadKeyboardState(ctrl, kbState)
			_ = ctrl.Close()
			if err != nil {
				// Not printed here as well: Execute prints the returned error
				// and exits 1, which is the whole point of returning it.
				failed = errors.Join(failed, fmt.Errorf("keyboard: %w", err))
			} else {
				fmt.Println("Keyboard reloaded.")
				reloaded = true
			}
		}
	}

	// Lightbar
	if lbState, ok := bundle["lightbar"].(map[string]any); ok && len(lbState) > 0 {
		if err := restoreLightbarState(lbState, nil); err != nil {
			fmt.Printf("Lightbar: %v, skipping.\n", err)
		} else {
			fmt.Println("Lightbar reloaded.")
			reloaded = true
		}
	}

	if !reloaded && failed == nil {
		fmt.Println("No saved state found.")
	}

	return failed
}

// reloadKeyboardState re-applies one saved keyboard state and reports what the
// hardware refused.
//
// Every write is attempted even after one fails — a brightness that did not
// land is no reason to skip the colours — so the errors are joined rather than
// returned at the first one. Discarding them was the actual defect: `avellcc
// reload` printed "Keyboard reloaded." and exited 0 with all five writes
// rejected, and the resume monitor's `|| true` then hid even the exit code.
func reloadKeyboardState(ctrl keyboard.Controller, kbState map[string]any) error {
	var failed error
	fail := func(what string, err error) {
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("%s: %w", what, err))
		}
	}

	mode, _ := kbState["mode"].(string)
	switch mode {
	case "off":
		fail("switching the backlight off", ctrl.Off())
	case "effect":
		effect, _ := kbState["effect"].(string)
		if effect != "" {
			speed := 3
			if s, ok := config.GetInt(kbState, "speed"); ok {
				speed = s
			}
			if animID, ok := ctrl.HWEffects()[strings.ToLower(effect)]; ok {
				fail("starting the hardware effect", ctrl.SetHWAnimation(animID, speed))
			} else {
				swName := strings.ToLower(effect)
				if !strings.HasPrefix(swName, "sw_") {
					swName = "sw_" + swName
				}
				if _, ok := keyboard.SoftwareEffects[swName]; ok {
					// Software effects render from a live process, so a one-shot
					// reload cannot restore one. Say so rather than starting a
					// runner that dies the moment this command returns.
					fmt.Printf("Keyboard: software effect %q needs a running process; "+
						"start it with 'avellcc keyboard --effect %s'.\n", effect, effect)
				}
			}
		}
	case "static":
		if colorArr, ok := kbState["color"].([]any); ok && len(colorArr) == 3 {
			r, _ := config.GetInt(map[string]any{"v": colorArr[0]}, "v")
			g, _ := config.GetInt(map[string]any{"v": colorArr[1]}, "v")
			b, _ := config.GetInt(map[string]any{"v": colorArr[2]}, "v")
			fail("painting every key", ctrl.SetAllKeys(byte(r), byte(g), byte(b)))
		}
	case "profile":
		profilePath, _ := kbState["profile"].(string)
		if profilePath != "" {
			_, runner, err := loadProfile(ctrl, profilePath)
			fail("loading the profile", err)
			if runner != nil {
				runner.Stop()
				fmt.Printf("Keyboard: profile %q contains a software effect, which "+
					"reload cannot keep running.\n", profilePath)
			}
		}
	}

	if brightness, ok := config.GetInt(kbState, "brightness"); ok {
		fail("setting the brightness", ctrl.SetBrightness(brightness))
	}

	if perKey, ok := kbState["per_key"].(map[string]any); ok {
		keymap := keyboard.LoadKeymapFor(ctrl)
		for keyName, colorVal := range perKey {
			if colorArr, ok := colorVal.([]any); ok && len(colorArr) == 3 {
				pos, found := keyboard.GetKeyPosition(keyName, keymap)
				if found {
					r, _ := config.GetInt(map[string]any{"v": colorArr[0]}, "v")
					g, _ := config.GetInt(map[string]any{"v": colorArr[1]}, "v")
					b, _ := config.GetInt(map[string]any{"v": colorArr[2]}, "v")
					fail("painting key "+keyName, ctrl.SetKeyColor(pos[0], pos[1], byte(r), byte(g), byte(b)))
				}
			}
		}
	}

	return failed
}

func restoreLightbarState(lbState map[string]any, ctrl *lightbar.ITE8911) error {
	if lbState == nil {
		return nil
	}

	// State written by the ITE 8233 chassis bar is RGB and carries its own
	// marker; it must not be read through the X58 palette below.
	if controller, _ := config.GetString(lbState, "controller"); controller == lb8233StateKey {
		return restoreLightbar8233State(lbState)
	}

	state := config.MergeLightbarState(lbState, nil)
	ownsCtrl := ctrl == nil
	if ctrl == nil {
		ctrl = lightbar.NewITE8911(nil)
		if err := ctrl.Open(); err != nil {
			return err
		}
	}
	if ownsCtrl {
		defer func() { _ = ctrl.Close() }()
	}

	mode, _ := state["mode"].(string)
	if mode == "off" {
		return ctrl.X58Off()
	}

	var effectCode *byte
	var colorID *byte
	var brightness *int
	var speed *byte

	if ec, ok := config.GetInt(state, "effect_code"); ok {
		b := byte(ec)
		effectCode = &b
	}
	if ci, ok := config.GetInt(state, "color_id"); ok {
		b := byte(ci)
		colorID = &b
	}
	if br, ok := config.GetInt(state, "brightness"); ok {
		brightness = &br
	}
	if sp, ok := config.GetInt(state, "speed"); ok {
		b := byte(sp)
		speed = &b
	}

	return ctrl.X58Apply(effectCode, colorID, brightness, speed)
}
