package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/keyboard"
)

// G08: `avellcc reload` printed "Keyboard reloaded." and exited 0 with every
// HID write rejected. The five writes were each discarded with `_ =`, the
// function returned nothing, and the caller announced success unconditionally
// — so a keyboard that came back blank after a resume looked exactly like one
// that came back right, in the terminal and in the journal.
//
// No hardware is touched here: deadController is a Controller whose every write
// fails, which is what a re-enumerated USB device looks like from this side.

type deadController struct {
	err   error
	calls int
}

var errDead = errors.New("no such device")

func (c *deadController) fail() error {
	c.calls++
	return c.err
}

func (c *deadController) Name() string { return "dead" }
func (c *deadController) Open() error  { return c.fail() }
func (c *deadController) Close() error { return nil }
func (c *deadController) Rows() int    { return 6 }
func (c *deadController) Cols() int    { return 21 }
func (c *deadController) KeymapID() string {
	return keyboard.KeymapIDITE8291
}
func (c *deadController) DefaultKeymap() map[string][2]int         { return keyboard.DefaultMap8291 }
func (c *deadController) HWEffects() map[string]int                { return keyboard.HWEffects8291 }
func (c *deadController) SetBrightness(int) error                  { return c.fail() }
func (c *deadController) SetKeyColor(_, _ int, _, _, _ byte) error { return c.fail() }
func (c *deadController) SetAllKeys(_, _, _ byte) error            { return c.fail() }
func (c *deadController) SetKeyMap(map[[2]int][3]byte) error       { return c.fail() }
func (c *deadController) SetHWAnimation(_, _ int) error            { return c.fail() }
func (c *deadController) Off() error                               { return c.fail() }
func (c *deadController) GetFirmwareInfo() ([]byte, error)         { return nil, c.fail() }

func TestReloadReportsWhatTheKeyboardRefused(t *testing.T) {
	// A throwaway config dir: the per-key path reads the calibrated keymap, and
	// a test must not depend on the one this machine happens to have.
	withKeyboardState(t)
	ctrl := &deadController{err: errDead}
	state := map[string]any{
		"mode":       "static",
		"color":      []any{float64(0x8A), float64(0xA4), float64(0xB0)},
		"brightness": float64(8),
		"per_key":    map[string]any{"esc": []any{float64(1), float64(2), float64(3)}},
	}

	err := reloadKeyboardState(ctrl, state)
	if err == nil {
		t.Fatal("every write failed and reload reported success")
	}
	if !errors.Is(err, errDead) {
		t.Errorf("the device's own error was swallowed: %v", err)
	}
	// The writes are all attempted: a brightness that did not land is no reason
	// to leave the colours unwritten.
	for _, want := range []string{"painting every key", "setting the brightness"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say which write failed (%q)", err, want)
		}
	}
	if ctrl.calls < 2 {
		t.Errorf("reload stopped after the first failure: %d writes attempted", ctrl.calls)
	}
}

// The off path is one write and it has to report too — that is the state the
// resume hook restores most often on a machine whose owner keeps the backlight
// down.
func TestReloadReportsAFailedOff(t *testing.T) {
	ctrl := &deadController{err: errDead}
	if err := reloadKeyboardState(ctrl, map[string]any{"mode": "off"}); err == nil {
		t.Fatal("a failed Off() was reported as a successful reload")
	}
}

// A controller that accepts everything still reports success, or the resume
// hook would start crying wolf on every wake.
func TestReloadStaysSilentWhenTheWritesLand(t *testing.T) {
	ctrl := &deadController{err: nil}
	state := map[string]any{
		"mode":       "static",
		"color":      []any{float64(1), float64(2), float64(3)},
		"brightness": float64(4),
	}
	if err := reloadKeyboardState(ctrl, state); err != nil {
		t.Fatalf("a reload that worked reported %v", err)
	}
}
