package tui

import (
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/keyboard"
)

// The panel wrote the state file with a bare LoadStateBundle/SaveStateBundle
// pair: no lock, and SaveStateBundle replaces the *whole* file, not just the
// keyboard half. The window between the two is as long as the user's next
// keystroke, and the theme hook, the now-playing hook and the pulse daemon all
// write the same file — so a light bar colour written while the panel was open
// was rolled back the next time the user pressed a key.
//
// No hardware: panelController answers the few questions the panel asks and
// writes nothing.

type panelController struct{}

func (panelController) Name() string                             { return "test" }
func (panelController) Open() error                              { return nil }
func (panelController) Close() error                             { return nil }
func (panelController) Rows() int                                { return 6 }
func (panelController) Cols() int                                { return 21 }
func (panelController) KeymapID() string                         { return keyboard.KeymapIDITE8291 }
func (panelController) DefaultKeymap() map[string][2]int         { return keyboard.DefaultMap8291 }
func (panelController) HWEffects() map[string]int                { return keyboard.HWEffects8291 }
func (panelController) SetBrightness(int) error                  { return nil }
func (panelController) SetKeyColor(_, _ int, _, _, _ byte) error { return nil }
func (panelController) SetAllKeys(_, _, _ byte) error            { return nil }
func (panelController) SetKeyMap(map[[2]int][3]byte) error       { return nil }
func (panelController) SetHWAnimation(_, _ int) error            { return nil }
func (panelController) Off() error                               { return nil }
func (panelController) GetFirmwareInfo() ([]byte, error)         { return nil, nil }

func TestPanelSaveDoesNotRollBackTheLightbarHalf(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	panel := NewKeyboardPanel(panelController{})

	// Each round is one keystroke in the panel racing one hook writing the bar.
	// Unlocked, the panel's save puts back the bundle it read before the hook
	// wrote, and the hook's colour is gone.
	for round := range 200 {
		if err := config.SaveLightbarState(map[string]any{
			"mode": "active", "brightness": float64(round % 100),
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			panel.saveState()
		}()
		go func() {
			defer wg.Done()
			if err := config.SaveLightbarState(map[string]any{
				"mode": "active", "brightness": float64(42),
			}); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()

		bundle := config.LoadStateBundle()
		lb, ok := bundle["lightbar"].(map[string]any)
		if !ok {
			t.Fatalf("round %d: the panel's save dropped the lightbar half entirely: %v",
				round, bundle)
		}
		if b, ok := config.GetInt(lb, "brightness"); !ok || b != 42 {
			t.Fatalf("round %d: lightbar brightness = %v, want the 42 written "+
				"alongside the panel's save; the panel rolled it back", round, lb["brightness"])
		}
		kb, _ := bundle["keyboard"].(map[string]any)
		if len(kb) == 0 {
			t.Fatalf("round %d: the panel's own half is missing: %v", round, bundle)
		}
	}
}

// The "o" key writes a different shape — the whole keyboard half replaced by
// mode=off — through the same file, and had the same defect.
func TestPanelOffDoesNotRollBackTheLightbarHalf(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	panel := NewKeyboardPanel(panelController{})
	for round := range 200 {
		if err := config.SaveLightbarState(map[string]any{
			"mode": "active", "brightness": float64(round % 100),
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// The panel itself, driven by the keystroke.
			panel.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
		}()
		go func() {
			defer wg.Done()
			if err := config.SaveLightbarState(map[string]any{
				"mode": "active", "brightness": float64(42),
			}); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()

		bundle := config.LoadStateBundle()
		lb, _ := bundle["lightbar"].(map[string]any)
		if b, ok := config.GetInt(lb, "brightness"); !ok || b != 42 {
			t.Fatalf("round %d: the lightbar half did not survive the keyboard "+
				"being switched off: %v", round, bundle)
		}
		if kb, _ := bundle["keyboard"].(map[string]any); len(kb) == 0 {
			t.Fatalf("round %d: the keyboard half is missing: %v", round, bundle)
		}
	}
}
