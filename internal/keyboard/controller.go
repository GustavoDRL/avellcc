package keyboard

import (
	"fmt"
	"sort"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// KeymapID* name the per-controller calibrated keymap files on disk.
const (
	KeymapIDITE8295 = "ite8295"
	KeymapIDITE8291 = "ite8291"
)

// MaxGrid* bound the largest LED grid any supported controller reports. They
// size layout buffers that must fit every controller; use the Controller's own
// Rows/Cols when driving hardware.
const (
	MaxGridRows = 6
	MaxGridCols = 21
)

// Controller drives a per-key RGB keyboard LED controller. It is implemented by
// ITE8295 (Clevo barebones) and ITE8291 (Uniwill/TongFang barebones), which
// speak different protocols over different grid sizes.
type Controller interface {
	// Name identifies the controller in user-facing output.
	Name() string
	// Open opens the underlying hidraw device, discovering it if needed.
	Open() error
	// Close releases the device.
	Close() error

	// Rows and Cols describe the LED grid this controller addresses.
	Rows() int
	Cols() int

	// SetBrightness sets the backlight level on a 0-10 scale, which each
	// driver maps onto its own hardware range.
	SetBrightness(level int) error
	// SetKeyColor sets one key by grid position.
	SetKeyColor(row, col int, r, g, b byte) error
	// SetAllKeys sets every key to one color.
	SetAllKeys(r, g, b byte) error
	// SetKeyMap applies a batch of per-position colors.
	SetKeyMap(colorMap map[[2]int][3]byte) error
	// SetHWAnimation starts a built-in animation; IDs come from HWEffects.
	// speed is 0-10; controllers without a speed parameter ignore it.
	SetHWAnimation(animID, speed int) error
	// HWEffects maps effect names to this controller's animation IDs.
	HWEffects() map[string]int

	// KeymapID names this controller's calibrated keymap file.
	KeymapID() string
	// DefaultKeymap is the built-in key name to grid position map.
	DefaultKeymap() map[string][2]int
	// Off turns the backlight off.
	Off() error
	// GetFirmwareInfo returns raw controller firmware bytes.
	GetFirmwareInfo() ([]byte, error)
}

// Compile-time proof that both drivers satisfy the interface.
var (
	_ Controller = (*ITE8295)(nil)
	_ Controller = (*ITE8291)(nil)
)

// NewController detects the keyboard LED controller present on this machine.
// The ITE 8291 is probed first because it identifies itself by a vendor HID
// collection, which is a stronger signal than a bare product ID match.
func NewController() (Controller, error) {
	if _, err := FindITE8291(); err == nil {
		return NewITE8291(nil), nil
	}
	if _, err := hidraw.FindHidraw(VID, PIDMain); err == nil {
		return NewITE8295(nil), nil
	}
	return nil, fmt.Errorf("no supported keyboard LED controller found "+
		"(looked for ITE 8291 %04x:%v and ITE 8295 %04x:%04x)",
		VID8291, formatPIDs(PIDs8291), VID, PIDMain)
}

func formatPIDs(pids []uint16) string {
	out := make([]string, len(pids))
	for i, p := range pids {
		out[i] = fmt.Sprintf("%04x", p)
	}
	return fmt.Sprintf("%v", out)
}

// AllHWEffectNames returns every hardware effect name across all controllers.
// Command-line help is built before a device is opened, so it cannot be limited
// to the controller actually present.
func AllHWEffectNames() []string {
	seen := map[string]bool{}
	for name := range EffectNames {
		seen[name] = true
	}
	for name := range HWEffects8291 {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
