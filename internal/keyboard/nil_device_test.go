package keyboard

import (
	"testing"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// NewITE8291(nil) and NewITE8295(nil) leave c.dev nil until Open() has
// succeeded, and every write went straight through it. This is the same defect
// that took the pulse daemon down on the chassis bar (the ITE 8233's Reopen
// leaves the pointer nil when it finds no device); it is latent here only
// because every path in cmd/ opens first and returns on the error, and neither
// of these controllers has a Reopen yet. A nil dereference takes the whole
// process with it, and this is one line per call site.
//
// No hardware is touched: the point is precisely that nothing was ever opened.

func TestITE8291WritesErrorWhenNothingWasOpened(t *testing.T) {
	// A throwaway config dir: nothing here may reach the real framebuffer
	// mirror in ~/.config/avellcc.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := NewITE8291(nil)

	for name, call := range map[string]func() error{
		"SetBrightness":   func() error { return c.SetBrightness(5) },
		"SetKeyColor":     func() error { return c.SetKeyColor(0, 0, 1, 2, 3) },
		"SetAllKeys":      func() error { return c.SetAllKeys(1, 2, 3) },
		"SetKeyMap":       func() error { return c.SetKeyMap(map[[2]int][3]byte{{0, 0}: {1, 2, 3}}) },
		"SetHWAnimation":  func() error { return c.SetHWAnimation(3, 5) },
		"SetPaletteColor": func() error { return c.SetPaletteColor(1, 1, 2, 3) },
		"Off":             func() error { return c.Off() },
		"GetFirmwareInfo": func() error {
			_, err := c.GetFirmwareInfo()
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s on an unopened controller reported success", name)
		}
	}
}

func TestITE8295WritesErrorWhenNothingWasOpened(t *testing.T) {
	c := NewITE8295(nil)

	for name, call := range map[string]func() error{
		"SetBrightness":  func() error { return c.SetBrightness(5) },
		"SetKeyColor":    func() error { return c.SetKeyColor(0, 0, 1, 2, 3) },
		"SetAllKeys":     func() error { return c.SetAllKeys(1, 2, 3) },
		"SetKeyMap":      func() error { return c.SetKeyMap(map[[2]int][3]byte{{0, 0}: {1, 2, 3}}) },
		"SetHWAnimation": func() error { return c.SetHWAnimation(3, 5) },
		"Off":            func() error { return c.Off() },
		"GetFirmwareInfo": func() error {
			_, err := c.GetFirmwareInfo()
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s on an unopened controller reported success", name)
		}
	}
}

// A controller built around a device that exists still behaves as before: the
// guard is about the nil pointer, not about the device being unreachable.
func TestAnExplicitDeviceIsNotTreatedAsUnopened(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := NewITE8291(&hidraw.HidrawDevice{Path: "/dev/null"})
	err := c.SetBrightness(5)
	if err == nil {
		t.Skip("/dev/null accepted a feature report; nothing to assert")
	}
	if got := err.Error(); got == "ITE 8291 keyboard is not open" {
		t.Errorf("a controller with a device reported %q", got)
	}
}
