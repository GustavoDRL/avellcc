package cmd

import (
	"testing"

	"github.com/hugo-andrade/avellcc/internal/config"
)

// `--effect wave` and then `--color X --key esc` used to leave mode=effect in
// the saved state, with the per-key colour beside it. On the ITE 8291 that is
// not what the keyboard is doing: a per-key write has to enter user mode
// (SET_EFFECT 51) first, and entering user mode is exactly what takes the
// animation away — docs/ite8291-protocol.md, "Per-key colour", and
// internal/keyboard/ite8291.go:enableUserMode, which every per-key write goes
// through. So the next `avellcc reload` restarted an animation the hardware had
// already dropped, and that animation owns every LED: it paints straight over
// the key the user had just set.
//
// Decided from the driver and the protocol document, not from the keyboard:
// writing to the real hardware is not allowed in this pass.

func TestPerKeyWriteEndsASavedEffectWhereTheDriverSaysItDoes(t *testing.T) {
	state := map[string]any{}
	stateSetBrightness(8)(state)
	stateSetEffect("wave", 4)(state)
	stateSetKeyColor("esc", 0xFF, 0x00, 0x00, true)(state)

	if mode, _ := config.GetString(state, "mode"); mode == actionEffect {
		t.Errorf("mode is still %q; reload would restart the animation this write "+
			"cancelled, and it would paint over the key: %v", mode, state)
	}
	if _, ok := state[actionEffect]; ok {
		t.Errorf("the effect name survived the per-key write: %v", state)
	}
	if _, ok := state["speed"]; ok {
		t.Errorf("the effect speed survived the per-key write: %v", state)
	}
	// What the write actually did has to still be there.
	perKey, _ := state["per_key"].(map[string]any)
	if _, ok := perKey["esc"]; !ok {
		t.Errorf("the key this command set is missing: %v", state)
	}
	if b, ok := config.GetInt(state, "brightness"); !ok || b != 8 {
		t.Errorf("the brightness was dropped: %v", state)
	}
	// No static colour is recorded, and that is deliberate: the grid is
	// whatever the controller's framebuffer holds, and reload repaints nothing.
	if _, ok := state["color"]; ok {
		t.Errorf("a static colour was invented that was never written: %v", state)
	}
}

// The ITE 8295's per-key command has no documented user mode and there is no
// such keyboard here to measure one on, so its saved state is left exactly as
// it was rather than guessed at.
func TestPerKeyWriteLeavesTheEffectAloneWhereTheDriverDoesNotSaySo(t *testing.T) {
	state := map[string]any{}
	stateSetEffect("wave", 4)(state)
	stateSetKeyColor("esc", 0xFF, 0x00, 0x00, false)(state)

	if mode, _ := config.GetString(state, "mode"); mode != actionEffect {
		t.Errorf("mode = %q, want the effect left untouched: %v", mode, state)
	}
	if effect, _ := config.GetString(state, actionEffect); effect != "wave" {
		t.Errorf("effect = %q, want wave: %v", effect, state)
	}
}

// A per-key write over a static colour still drops nothing: the base colour is
// genuinely still lit under it.
func TestPerKeyWriteKeepsAStaticColour(t *testing.T) {
	state := map[string]any{}
	stateSetColorAll(0x01, 0x02, 0x03)(state)
	stateSetKeyColor("esc", 0xFF, 0x00, 0x00, true)(state)

	if _, ok := state["color"]; !ok {
		t.Errorf("the static colour under the key was dropped: %v", state)
	}
	if mode, _ := config.GetString(state, "mode"); mode != "static" {
		t.Errorf("mode = %q, want static: %v", mode, state)
	}
}
