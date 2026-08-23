package cmd

import (
	"testing"

	"github.com/hugo-andrade/avellcc/internal/config"
)

// Regressions for the state every keyboard command used to throw away. The
// command built a fresh map and wrote it over the saved one, so `--color` lost
// the brightness, each `--key` lost the key before it, and the reload that
// follows had nothing left to restore. These exercise the save path itself —
// the mutators plus the locked load-modify-save — which is everything between
// the device write and the file.

func withKeyboardState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func savedKeyboard(t *testing.T) map[string]any {
	t.Helper()
	state, _ := config.LoadStateBundle()["keyboard"].(map[string]any)
	return state
}

// The case from the report: `avellcc keyboard --color X` must not forget the
// brightness that was written before it, or reload stops calling SetBrightness
// at all and the keyboard comes back at the firmware default.
func TestColorKeepsTheSavedBrightness(t *testing.T) {
	withKeyboardState(t)

	if err := saveKeyboardState([]func(map[string]any){stateSetBrightness(8)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := saveKeyboardState([]func(map[string]any){stateSetColorAll(0xFF, 0x00, 0x00)}, nil); err != nil {
		t.Fatal(err)
	}

	state := savedKeyboard(t)
	brightness, ok := config.GetInt(state, "brightness")
	if !ok {
		t.Fatalf("--color erased the saved brightness; state is %v", state)
	}
	if brightness != 8 {
		t.Errorf("brightness = %d, want 8", brightness)
	}
	if mode, _ := config.GetString(state, "mode"); mode != "static" {
		t.Errorf("mode = %q, want static", mode)
	}
}

// Each --key colours one key. Two of them in a row have to leave two keys
// coloured, which is what the hardware is actually showing.
func TestPerKeyColoursAccumulate(t *testing.T) {
	withKeyboardState(t)

	if err := saveKeyboardState([]func(map[string]any){stateSetBrightness(6)}, nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if err := saveKeyboardState([]func(map[string]any){stateSetKeyColor(key, 0x10, 0x20, 0x30)}, nil); err != nil {
			t.Fatal(err)
		}
	}

	state := savedKeyboard(t)
	perKey, _ := state["per_key"].(map[string]any)
	if len(perKey) != 2 {
		t.Errorf("per_key = %v, want both keys", perKey)
	}
	if b, ok := config.GetInt(state, "brightness"); !ok || b != 6 {
		t.Errorf("--key erased the brightness: %v", state)
	}
}

// The other half of the fix: what a command does invalidate, it invalidates on
// purpose. An animation owns every LED, so the colours saved before it are no
// longer on the keyboard and reload must not repaint them over it.
func TestAnEffectDropsTheColoursItPaintedOver(t *testing.T) {
	withKeyboardState(t)

	updates := []func(map[string]any){
		stateSetBrightness(5),
		stateSetColorAll(0x01, 0x02, 0x03),
		stateSetKeyColor("esc", 0xFF, 0xFF, 0xFF),
	}
	if err := saveKeyboardState(updates, nil); err != nil {
		t.Fatal(err)
	}
	if err := saveKeyboardState([]func(map[string]any){stateSetEffect("wave", 4)}, nil); err != nil {
		t.Fatal(err)
	}

	state := savedKeyboard(t)
	if _, ok := state["color"]; ok {
		t.Error("the static colour survived an effect")
	}
	if _, ok := state["per_key"]; ok {
		t.Error("the per-key colours survived an effect")
	}
	if b, ok := config.GetInt(state, "brightness"); !ok || b != 5 {
		t.Errorf("the effect erased the brightness: %v", state)
	}
	if effect, _ := config.GetString(state, "effect"); effect != "wave" {
		t.Errorf("effect = %q, want wave", effect)
	}
}

// --off is the one command that does replace everything: reload applies the
// brightness and the per-key colours after ctrl.Off(), so anything left behind
// would light the keyboard straight back up.
func TestOffLeavesNothingThatWouldRelightTheKeyboard(t *testing.T) {
	withKeyboardState(t)

	updates := []func(map[string]any){
		stateSetBrightness(9),
		stateSetColorAll(0x01, 0x02, 0x03),
		stateSetKeyColor("esc", 0xFF, 0xFF, 0xFF),
	}
	if err := saveKeyboardState(updates, nil); err != nil {
		t.Fatal(err)
	}
	if err := saveKeyboardState([]func(map[string]any){stateOff()}, nil); err != nil {
		t.Fatal(err)
	}

	state := savedKeyboard(t)
	if len(state) != 1 {
		t.Errorf("off left more than the mode behind: %v", state)
	}
	if mode, _ := config.GetString(state, "mode"); mode != "off" {
		t.Errorf("mode = %q, want off", mode)
	}
}

// A profile carries its own brightness, and reload applies the state's
// brightness after loading the profile — so a leftover value would override the
// profile's. It survives only when it was given on the same command line.
func TestProfileDropsAStaleBrightnessButKeepsAnExplicitOne(t *testing.T) {
	withKeyboardState(t)

	if err := saveKeyboardState([]func(map[string]any){stateSetBrightness(9)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := saveKeyboardState([]func(map[string]any){stateSetProfile("neon.json", false)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := config.GetInt(savedKeyboard(t), "brightness"); ok {
		t.Error("a profile kept the brightness from before it")
	}

	explicit := []func(map[string]any){
		stateSetProfile("neon.json", true),
		stateSetBrightness(4),
	}
	if err := saveKeyboardState(explicit, nil); err != nil {
		t.Fatal(err)
	}
	if b, ok := config.GetInt(savedKeyboard(t), "brightness"); !ok || b != 4 {
		t.Errorf("--profile --brightness 4 did not keep the brightness: %v", savedKeyboard(t))
	}
}

// The lightbar half of a profile has to land in the same write as the keyboard
// half, and neither half may drop the other.
func TestSavingBothHalvesKeepsBoth(t *testing.T) {
	withKeyboardState(t)

	if err := config.SaveLightbarState(map[string]any{"mode": "active", "brightness": float64(30)}); err != nil {
		t.Fatal(err)
	}
	if err := saveKeyboardState([]func(map[string]any){stateSetBrightness(7)}, nil); err != nil {
		t.Fatal(err)
	}

	bundle := config.LoadStateBundle()
	lb, _ := bundle["lightbar"].(map[string]any)
	if b, ok := config.GetInt(lb, "brightness"); !ok || b != 30 {
		t.Errorf("a keyboard write dropped the lightbar half: %v", bundle)
	}
	kb, _ := bundle["keyboard"].(map[string]any)
	if b, ok := config.GetInt(kb, "brightness"); !ok || b != 7 {
		t.Errorf("the keyboard half did not land: %v", bundle)
	}
}

// TestBrightnessAfterOffDoesNotStayOff pins the contradiction that reload
// resolves the wrong way: ctrl.Off() runs first and SetBrightness after it, so a
// state carrying both mode=off and a brightness lights the keyboard back up.
func TestBrightnessAfterOffDoesNotStayOff(t *testing.T) {
	state := map[string]any{}
	stateOff()(state)
	stateSetBrightness(20)(state)

	if mode, _ := state["mode"].(string); mode == actionOff {
		t.Fatalf("--brightness after --off left mode=off; reload would call Off() then SetBrightness and light it back up: %v", state)
	}
	if b, ok := state["brightness"].(float64); !ok || b != 20 {
		t.Fatalf("brightness was not kept: %v", state)
	}
}
