// Package keyboard implements the ITE IT8295 per-key RGB keyboard controller.
package keyboard

import (
	"fmt"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

const (
	VID     = 0x048D
	PIDMain = 0x8910

	ReportID   = 0xCC
	ReportSize = 7 // report_id + 6 bytes

	GridRows = 6
	GridCols = 20

	CmdSetEffect     = 0x00
	CmdSetKeyColor   = 0x01
	CmdSetBrightness = 0x09

	HWAnimRandomColor = 0x09
	MaxBrightness     = 10
)

// EffectNames maps effect names to hardware animation sub-commands.
var EffectNames = map[string]int{
	"random_color": HWAnimRandomColor,
	"rainbow":      HWAnimRandomColor,
}

// LedID computes the LED ID from grid coordinates.
func LedID(row, col int) int {
	return ((row & 0x07) << 5) | (col & 0x1F)
}

// ITE8295 drives the ITE IT8295 per-key RGB keyboard controller.
type ITE8295 struct {
	dev     *hidraw.HidrawDevice
	ownsDev bool
}

// NewITE8295 creates a new controller. If dev is nil, it auto-discovers the device.
func NewITE8295(dev *hidraw.HidrawDevice) *ITE8295 {
	return &ITE8295{dev: dev, ownsDev: dev == nil}
}

// Open opens the hidraw device (auto-discovers if needed).
func (c *ITE8295) Open() error {
	if c.dev == nil {
		path, err := hidraw.FindHidraw(VID, PIDMain)
		if err != nil {
			return fmt.Errorf("ITE 8295 device (%04x:%04x) not found: %w", VID, PIDMain, err)
		}
		c.dev = &hidraw.HidrawDevice{Path: path}
		c.ownsDev = true
	}
	return c.dev.Open()
}

// Close closes the hidraw device if owned.
func (c *ITE8295) Close() error {
	if c.dev != nil && c.ownsDev {
		return c.dev.Close()
	}
	return nil
}

func (c *ITE8295) send(cmd, a1, a2, a3, a4 int) error {
	buf := []byte{ReportID, byte(cmd), byte(a1), byte(a2), byte(a3), byte(a4), 0x00}
	return c.dev.SendFeatureReport(buf)
}

// SetBrightness sets keyboard backlight brightness (0-10).
func (c *ITE8295) SetBrightness(level int) error {
	if level < 0 {
		level = 0
	}
	if level > MaxBrightness {
		level = MaxBrightness
	}
	return c.send(CmdSetBrightness, level, 0x02, 0, 0)
}

// SetKeyColor sets the color of a single key by grid position.
func (c *ITE8295) SetKeyColor(row, col int, r, g, b byte) error {
	return c.send(CmdSetKeyColor, LedID(row, col), int(r), int(g), int(b))
}

// SetAllKeys sets all keys to the same color.
func (c *ITE8295) SetAllKeys(r, g, b byte) error {
	for row := 0; row < GridRows; row++ {
		for col := 0; col < GridCols; col++ {
			if err := c.SetKeyColor(row, col, r, g, b); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetKeyMap sets colors from a map of grid positions to RGB values.
func (c *ITE8295) SetKeyMap(colorMap map[[2]int][3]byte) error {
	for pos, rgb := range colorMap {
		if err := c.SetKeyColor(pos[0], pos[1], rgb[0], rgb[1], rgb[2]); err != nil {
			return err
		}
	}
	return nil
}

// SetHWAnimation triggers a hardware-driven animation effect. This controller's
// animation command carries no speed field, so speed is ignored.
func (c *ITE8295) SetHWAnimation(animID, _ int) error {
	return c.send(CmdSetEffect, animID, 0, 0, 0)
}

// Off turns off all keyboard LEDs.
func (c *ITE8295) Off() error {
	return c.SetBrightness(0)
}

// GetFirmwareInfo reads firmware info from report 0x5A.
func (c *ITE8295) GetFirmwareInfo() ([]byte, error) {
	return c.dev.GetFeatureReport(0x5A, 17)
}

// Name identifies the controller in user-facing output.
func (c *ITE8295) Name() string { return "ITE 8295" }

// Rows returns the LED grid height.
func (c *ITE8295) Rows() int { return GridRows }

// Cols returns the LED grid width.
func (c *ITE8295) Cols() int { return GridCols }

// HWEffects returns the controller's built-in animations.
func (c *ITE8295) HWEffects() map[string]int { return EffectNames }

// KeymapID names this controller's calibrated keymap file.
func (c *ITE8295) KeymapID() string { return KeymapIDITE8295 }

// DefaultKeymap returns the built-in 6x20 key map.
func (c *ITE8295) DefaultKeymap() map[string][2]int { return DefaultMap8295 }
