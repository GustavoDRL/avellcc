package config

import (
	"testing"

	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

// MergeLightbarState decides both what is saved AND what is sent: runLightbar
// applies the map that comes out of here. So a merge that discards the code the
// user just gave is not a display bug — the wrong effect reaches the device.

// The one the audit found. `avellcc lightbar --effect-code 7` on a state that
// already said effect="breathe" used to come back as effect_code 6: the saved
// NAME re-derived the code and overwrote the new one.
func TestAnUpdatedCodeIsNotUndoneByTheSavedName(t *testing.T) {
	state := map[string]any{"effect": "breathe", "effect_code": float64(lightbar.X58EffectCodes["breathe"])}
	updates := map[string]any{"mode": "active", "effect_code": float64(lightbar.X58EffectCodes["wave"])}

	merged := MergeLightbarState(state, updates)

	code, ok := GetInt(merged, "effect_code")
	if !ok {
		t.Fatalf("no effect_code in %v", merged)
	}
	if byte(code) != lightbar.X58EffectCodes["wave"] {
		t.Errorf("effect_code came out %#x, want %#x — the saved name overwrote the update",
			code, lightbar.X58EffectCodes["wave"])
	}
	if merged["effect"] != "wave" {
		t.Errorf("effect came out %q, want \"wave\" — the name disagrees with the code that was sent",
			merged["effect"])
	}
}

// Every named effect, both directions, so the fix cannot be right for one code
// and wrong for the rest.
func TestEveryEffectCodeUpdateCarriesItsName(t *testing.T) {
	for name, code := range lightbar.X58EffectCodes {
		for savedName, savedCode := range lightbar.X58EffectCodes {
			state := map[string]any{"effect": savedName, "effect_code": float64(savedCode)}
			merged := MergeLightbarState(state, map[string]any{"effect_code": float64(code)})

			got, _ := GetInt(merged, "effect_code")
			if byte(got) != code {
				t.Errorf("saved %s, asked for %s: effect_code came out %#x, want %#x",
					savedName, name, got, code)
			}
			if merged["effect"] != name {
				t.Errorf("saved %s, asked for %s: effect came out %q, want %q",
					savedName, name, merged["effect"], name)
			}
		}
	}
}

// A raw code with no name has to blank the name rather than keep the old one.
// Showing "breathe" next to a code that is not breathe is the same lie in a
// quieter form.
func TestARawCodeWithNoNameDoesNotKeepTheOldName(t *testing.T) {
	if _, taken := lightbar.X58EffectNames[0x7F]; taken {
		t.Skip("0x7F now names an effect; this test needs an unnamed code")
	}
	state := map[string]any{"effect": "breathe", "effect_code": float64(lightbar.X58EffectCodes["breathe"])}
	merged := MergeLightbarState(state, map[string]any{"effect_code": float64(0x7F)})

	if code, _ := GetInt(merged, "effect_code"); code != 0x7F {
		t.Errorf("effect_code came out %#x, want 0x7f", code)
	}
	if merged["effect"] == "breathe" {
		t.Error("the stale name survived a code it does not describe")
	}
	if merged["effect"] != "?" {
		t.Errorf("effect came out %q, want \"?\"", merged["effect"])
	}
}

// The reload path passes updates=nil, and there the saved NAME is still what
// repairs a bad code. Losing that would trade one defect for another.
func TestWithNoUpdatesTheSavedNameStillRepairsTheCode(t *testing.T) {
	state := map[string]any{"effect": "wave", "effect_code": float64(0x7F)}
	merged := MergeLightbarState(state, nil)

	code, _ := GetInt(merged, "effect_code")
	if byte(code) != lightbar.X58EffectCodes["wave"] {
		t.Errorf("effect_code came out %#x, want %#x — the saved name no longer repairs it",
			code, lightbar.X58EffectCodes["wave"])
	}
	if merged["effect"] != "wave" {
		t.Errorf("effect came out %q, want \"wave\"", merged["effect"])
	}
}

// A name given in the updates still decides the code, which is what the
// name-driven callers rely on.
func TestAnUpdatedNameStillDecidesTheCode(t *testing.T) {
	state := map[string]any{"effect": "breathe", "effect_code": float64(lightbar.X58EffectCodes["breathe"])}
	merged := MergeLightbarState(state, map[string]any{"effect": "granular"})

	code, _ := GetInt(merged, "effect_code")
	if byte(code) != lightbar.X58EffectCodes["granular"] {
		t.Errorf("effect_code came out %#x, want %#x",
			code, lightbar.X58EffectCodes["granular"])
	}
	if merged["effect"] != "granular" {
		t.Errorf("effect came out %q, want \"granular\"", merged["effect"])
	}
}

// Nothing outside the effect pair may move: the merge is also what preserves
// the settings the user did not touch.
func TestAnEffectCodeUpdateLeavesTheOtherFieldsAlone(t *testing.T) {
	state := map[string]any{
		"mode": "active", "effect": "breathe", "effect_code": float64(0x06),
		"color_id": float64(4), "brightness": float64(37), "speed": float64(9),
	}
	merged := MergeLightbarState(state, map[string]any{"effect_code": float64(0x0A)})

	for key, want := range map[string]int{"color_id": 4, "brightness": 37, "speed": 9} {
		if got, _ := GetInt(merged, key); got != want {
			t.Errorf("%s moved to %d, want %d", key, got, want)
		}
	}
}
