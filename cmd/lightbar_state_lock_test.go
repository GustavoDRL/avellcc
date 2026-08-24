package cmd

import (
	"fmt"
	"sync"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/config"
)

// The chassis-bar command did a load-modify-save with a HID transfer in the
// middle of it and no lock around any of it: `state := loadLightbar8233State()`
// at the top, `config.SaveLightbarState(state)` after the device write. Save
// replaces the whole "lightbar" map, so everything another writer put there in
// between was rolled back to what the file said before the write — and the
// device write is the slow part. There are four other writers of this file: the
// theme hook, the now-playing hook, the pulse daemon restoring the bar, and
// `avellcc reload`.

func TestConcurrentChassisWritesNeverLoseAField(t *testing.T) {
	withKeyboardState(t)

	const writers = 24
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := updateLightbar8233State(map[string]any{
				fmt.Sprintf("probe_%02d", i): float64(i),
			}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	state := loadLightbar8233State()
	var lost []string
	for i := range writers {
		key := fmt.Sprintf("probe_%02d", i)
		if got, ok := config.GetInt(state, key); !ok || got != i {
			lost = append(lost, key)
		}
	}
	if len(lost) > 0 {
		t.Errorf("%d of %d writes were lost (%v); the load-modify-save is not "+
			"inside the state lock", len(lost), writers, lost)
	}
}

// The keyboard half belongs to a different writer entirely and must survive a
// chassis-bar write, as must the marker that says which controller wrote this.
func TestAChassisWriteKeepsTheKeyboardHalfAndTheMarker(t *testing.T) {
	withKeyboardState(t)

	if err := saveKeyboardState([]func(map[string]any){stateSetBrightness(7)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := updateLightbar8233State(map[string]any{"color": "#8aa4b0"}); err != nil {
		t.Fatal(err)
	}

	bundle := config.LoadStateBundle()
	kb, _ := bundle["keyboard"].(map[string]any)
	if b, ok := config.GetInt(kb, "brightness"); !ok || b != 7 {
		t.Errorf("the chassis write dropped the keyboard half: %v", bundle)
	}
	lb, _ := bundle["lightbar"].(map[string]any)
	if marker, _ := config.GetString(lb, "controller"); marker != lb8233StateKey {
		t.Errorf("controller marker = %q, want %q: %v", marker, lb8233StateKey, bundle)
	}
	if color, _ := config.GetString(lb, "color"); color != "#8aa4b0" {
		t.Errorf("color = %q, want #8aa4b0: %v", color, bundle)
	}
}

// The ITE 8911 half of the same command had the same shape: LoadStateBundle at
// the top, a device write, SaveLightbarState with the map read before it.
func TestConcurrentX58WritesNeverLoseAField(t *testing.T) {
	withKeyboardState(t)

	const writers = 24
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := updateLightbarX58State(map[string]any{
				fmt.Sprintf("probe_%02d", i): float64(i),
			}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	state, _ := config.LoadStateBundle()["lightbar"].(map[string]any)
	var lost []string
	for i := range writers {
		key := fmt.Sprintf("probe_%02d", i)
		if got, ok := config.GetInt(state, key); !ok || got != i {
			lost = append(lost, key)
		}
	}
	if len(lost) > 0 {
		t.Errorf("%d of %d writes were lost (%v); the load-modify-save is not "+
			"inside the state lock", len(lost), writers, lost)
	}
}

// State left behind by the ITE 8911 driver is a different shape and must not be
// merged into this one — the same rule loadLightbar8233State applies on read.
func TestAChassisWriteDoesNotInheritTheOtherControllersState(t *testing.T) {
	withKeyboardState(t)

	if err := config.SaveLightbarState(map[string]any{
		"mode": "active", "color_id": float64(3), "effect_code": float64(2),
	}); err != nil {
		t.Fatal(err)
	}
	if err := updateLightbar8233State(map[string]any{"color": "#112233"}); err != nil {
		t.Fatal(err)
	}

	state := loadLightbar8233State()
	if _, ok := state["color_id"]; ok {
		t.Errorf("the ITE 8911 palette state was merged into the RGB state: %v", state)
	}
}
