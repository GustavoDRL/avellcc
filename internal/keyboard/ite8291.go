package keyboard

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// ITE 8291 rev 3 per-key RGB controller, found in Uniwill/TongFang barebones
// such as the Avell Storm 470. It is a different part from the Clevo ITE 8295:
// colours are pushed as whole rows through a 64-byte HID output report instead
// of one feature report per key, and per-key control only takes effect after
// the controller is switched into "user mode".
const (
	VID8291 = 0x048D

	Rows8291 = 6
	Cols8291 = 21

	// The vendor interface declares an 8-byte feature report and a 64-byte
	// output report, both unnumbered, so every hidraw buffer carries a
	// leading zero report-ID byte that the kernel strips.
	ctrlLen8291 = 1 + 8
	rowLen8291  = 1 + 64

	cmd8291SetEffect   = 0x08
	cmd8291SetBright   = 0x09
	cmd8291SetPalette  = 0x14
	cmd8291SetRowIndex = 0x16
	cmd8291GetFirmware = 0x80
	cmd8291GetEffect   = 0x88

	// Effect slot 51 is the "user mode" that hands per-key control to the host.
	effect8291UserMode = 51
	// Hardware brightness is 0-50; avellcc exposes 0-10 on every controller.
	hwMaxBrightness8291 = 50

	ctrl8291Off   = 0x01
	ctrl8291Apply = 0x02
)

// PIDs8291 lists the product IDs that speak the ITE 8291 rev 3 protocol.
var PIDs8291 = []uint16{0x6004, 0x6006, 0x600B, 0xCE00}

// HWEffects8291 maps effect names to the controller's built-in animations.
var HWEffects8291 = map[string]int{
	"breathing": 0x02,
	"wave":      0x03,
	"random":    0x04,
	"rainbow":   0x05,
	"ripple":    0x06,
	"marquee":   0x09,
	"raindrop":  0x0A,
	"aurora":    0x0E,
	"fireworks": 0x11,
}

// vendorPagePrefix identifies the vendor collection (usage page 0xFF03) that
// carries the LED protocol. The same USB device also exposes a plain keyboard
// interface, which must not be written to.
var vendorPagePrefix = []byte{0x06, 0x03, 0xFF}

// ITE8291 drives the ITE 8291 rev 3 per-key RGB keyboard controller.
type ITE8291 struct {
	dev     *hidraw.HidrawDevice
	ownsDev bool

	// fb mirrors the controller's LED state so a single key can be changed
	// without the caller having to resend the whole keyboard.
	fb [Rows8291][Cols8291][3]byte

	// hwBright is the brightness on the CONTROLLER's own 0-50 scale, not on
	// the 0-10 scale the CLI speaks. Keeping the 0-10 value here instead cost
	// up to four hardware steps per open: readState quantised the 0-50 it had
	// just read down to 0-10 (48 -> 9) and enableUserMode wrote it back out
	// (9 -> 45), so every hook and every resume darkened the keyboard a little
	// more until it settled on a multiple of five. The conversion belongs at
	// the CLI boundary — SetBrightness — and nowhere else.
	hwBright byte
	userMode bool // whether per-key control is currently active

	lastSave time.Time
}

// fbSaveInterval throttles framebuffer writes so software effects, which
// repaint at frame rate, do not hammer the disk.
const fbSaveInterval = 500 * time.Millisecond

// framebufferPath is where the mirrored LED state lives between runs.
func framebufferPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "avellcc", "ite8291-framebuffer.bin")
}

// loadFramebuffer restores the mirrored LED state saved by a previous run.
// Without it every process would start from an all-black framebuffer and
// setting one key would blank the rest of its row.
func (c *ITE8291) loadFramebuffer() {
	data, err := os.ReadFile(framebufferPath())
	if err != nil || len(data) != Rows8291*Cols8291*3 {
		return
	}
	i := 0
	for row := 0; row < Rows8291; row++ {
		for col := 0; col < Cols8291; col++ {
			c.fb[row][col] = [3]byte{data[i], data[i+1], data[i+2]}
			i += 3
		}
	}
}

// saveFramebuffer mirrors the LED state to disk. force bypasses the throttle.
func (c *ITE8291) saveFramebuffer(force bool) {
	now := time.Now()
	if !force && now.Sub(c.lastSave) < fbSaveInterval {
		return
	}
	c.lastSave = now

	data := make([]byte, 0, Rows8291*Cols8291*3)
	for row := 0; row < Rows8291; row++ {
		for col := 0; col < Cols8291; col++ {
			data = append(data, c.fb[row][col][0], c.fb[row][col][1], c.fb[row][col][2])
		}
	}
	path := framebufferPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// NewITE8291 creates a new controller. If dev is nil, it auto-discovers the device.
func NewITE8291(dev *hidraw.HidrawDevice) *ITE8291 {
	return &ITE8291{dev: dev, ownsDev: dev == nil, hwBright: hwBrightness(MaxBrightness)}
}

// Name identifies the controller in user-facing output.
func (c *ITE8291) Name() string { return "ITE 8291" }

// Rows returns the LED grid height.
func (c *ITE8291) Rows() int { return Rows8291 }

// Cols returns the LED grid width.
func (c *ITE8291) Cols() int { return Cols8291 }

// HWEffects returns the controller's built-in animations.
func (c *ITE8291) HWEffects() map[string]int { return HWEffects8291 }

// FindITE8291 locates the hidraw node carrying the LED vendor interface.
func FindITE8291() (string, error) {
	paths, err := hidraw.FindHidrawAll(VID8291, PIDs8291...)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		desc, err := hidraw.ReportDescriptor(path)
		if err != nil {
			continue
		}
		if bytes.HasPrefix(desc, vendorPagePrefix) {
			return path, nil
		}
	}
	if len(paths) > 0 {
		return "", fmt.Errorf("ITE 8291 found at %v but none exposes the LED vendor interface", paths)
	}
	return "", fmt.Errorf("no ITE 8291 device found (vendor %04x)", VID8291)
}

// Open opens the hidraw device (auto-discovers if needed).
func (c *ITE8291) Open() error {
	if c.dev == nil {
		path, err := FindITE8291()
		if err != nil {
			return err
		}
		c.dev = &hidraw.HidrawDevice{Path: path}
		c.ownsDev = true
	}
	if err := c.dev.Open(); err != nil {
		return err
	}
	c.loadFramebuffer()
	c.readState()
	return nil
}

// readState asks the controller what it is currently doing, so a freshly
// started process inherits the real brightness and knows whether per-key mode
// is already active instead of assuming defaults and repainting needlessly.
func (c *ITE8291) readState() {
	if err := c.sendCtrl(cmd8291GetEffect); err != nil {
		return
	}
	buf, err := c.getCtrl()
	if err != nil || len(buf) < 5 {
		return
	}
	// Reply layout: [echo, control, effect, speed, brightness, colour, ...]
	// buf[4] is a byte, so the old `hw >= 0` half of this test could never
	// fail; only the upper bound tells a real reply from a garbled one.
	control, effect, hw := buf[1], buf[2], buf[4]
	if hw <= hwMaxBrightness8291 {
		c.hwBright = hw
	}
	c.userMode = control != ctrl8291Off && effect == effect8291UserMode
}

// Close closes the hidraw device if owned.
func (c *ITE8291) Close() error {
	if c.dev != nil && c.ownsDev {
		c.saveFramebuffer(true)
		return c.dev.Close()
	}
	return nil
}

// sendCtrl sends one 8-byte command as a HID feature report.
//
// The nil check is the same one ITE8233.send carries: NewITE8291(nil) leaves
// c.dev nil until Open() has succeeded, and an error is something a caller can
// report while a nil dereference takes the whole process down.
func (c *ITE8291) sendCtrl(payload ...byte) error {
	if c.dev == nil {
		return fmt.Errorf("ITE 8291 keyboard is not open")
	}
	if len(payload) > ctrlLen8291-1 {
		return fmt.Errorf("command too long: %d bytes", len(payload))
	}
	buf := make([]byte, ctrlLen8291)
	copy(buf[1:], payload)
	return c.dev.SendFeatureReport(buf)
}

// getCtrl reads the 8-byte reply to the last command.
func (c *ITE8291) getCtrl() ([]byte, error) {
	if c.dev == nil {
		return nil, fmt.Errorf("ITE 8291 keyboard is not open")
	}
	buf, err := c.dev.GetFeatureReport(0x00, ctrlLen8291)
	if err != nil {
		return nil, err
	}
	return buf[1:], nil
}

// hwBrightness converts the 0-10 CLI scale to the controller's 0-50 range.
func hwBrightness(level int) byte {
	if level < 0 {
		level = 0
	}
	if level > MaxBrightness {
		level = MaxBrightness
	}
	return byte(level * hwMaxBrightness8291 / MaxBrightness)
}

// enableUserMode hands per-key control to the host. Hardware animations and
// Off() take it away again, so colour writes re-assert it.
// It reports whether it had to switch modes, which tells the caller the
// controller's LED buffer was discarded and needs a full repaint.
func (c *ITE8291) enableUserMode() (bool, error) {
	if c.userMode {
		return false, nil
	}
	err := c.sendCtrl(cmd8291SetEffect, ctrl8291Apply, effect8291UserMode,
		0x00, c.hwBright, 0x00, 0x00, 0x00)
	if err != nil {
		return false, err
	}
	c.userMode = true
	return true, nil
}

// flushRow pushes one row of the framebuffer to the controller.
func (c *ITE8291) flushRow(row int) error {
	if c.dev == nil {
		return fmt.Errorf("ITE 8291 keyboard is not open")
	}
	if err := c.sendCtrl(cmd8291SetRowIndex, 0x00, byte(row)); err != nil {
		return err
	}
	// Wire layout of the 64-byte report: all blues, all greens, all reds,
	// then one padding byte.
	buf := make([]byte, rowLen8291)
	for col := 0; col < Cols8291; col++ {
		buf[1+0*Cols8291+col] = c.fb[row][col][2]
		buf[1+1*Cols8291+col] = c.fb[row][col][1]
		buf[1+2*Cols8291+col] = c.fb[row][col][0]
	}
	return c.dev.Write(buf)
}

func (c *ITE8291) flushAll() error {
	for row := 0; row < Rows8291; row++ {
		if err := c.flushRow(row); err != nil {
			return err
		}
	}
	return nil
}

// SetBrightness sets keyboard backlight brightness (0-10).
func (c *ITE8291) SetBrightness(level int) error {
	if level < 0 {
		level = 0
	}
	if level > MaxBrightness {
		level = MaxBrightness
	}
	c.hwBright = hwBrightness(level)
	return c.sendCtrl(cmd8291SetBright, ctrl8291Apply, c.hwBright)
}

// SetKeyColor sets the color of a single key by grid position.
func (c *ITE8291) SetKeyColor(row, col int, r, g, b byte) error {
	if row < 0 || row >= Rows8291 || col < 0 || col >= Cols8291 {
		return fmt.Errorf("position out of range: row=%d col=%d", row, col)
	}
	repaint, err := c.enableUserMode()
	if err != nil {
		return err
	}
	c.fb[row][col] = [3]byte{r, g, b}
	// Coming back from an animation or from off leaves the controller's LED
	// buffer undefined, so the whole grid goes out rather than one row.
	if repaint {
		err = c.flushAll()
	} else {
		err = c.flushRow(row)
	}
	if err != nil {
		return err
	}
	c.saveFramebuffer(false)
	return nil
}

// SetAllKeys sets all keys to the same color.
func (c *ITE8291) SetAllKeys(r, g, b byte) error {
	if _, err := c.enableUserMode(); err != nil {
		return err
	}
	for row := 0; row < Rows8291; row++ {
		for col := 0; col < Cols8291; col++ {
			c.fb[row][col] = [3]byte{r, g, b}
		}
	}
	if err := c.flushAll(); err != nil {
		return err
	}
	c.saveFramebuffer(false)
	return nil
}

// SetKeyMap sets colors from a map of grid positions to RGB values. Each
// affected row is sent once, which keeps whole-keyboard updates cheap enough
// for software effects.
func (c *ITE8291) SetKeyMap(colorMap map[[2]int][3]byte) error {
	repaint, err := c.enableUserMode()
	if err != nil {
		return err
	}
	dirty := map[int]bool{}
	for pos, rgb := range colorMap {
		row, col := pos[0], pos[1]
		if row < 0 || row >= Rows8291 || col < 0 || col >= Cols8291 {
			continue
		}
		c.fb[row][col] = rgb
		dirty[row] = true
	}
	for row := 0; row < Rows8291; row++ {
		if !dirty[row] && !repaint {
			continue
		}
		if err := c.flushRow(row); err != nil {
			return err
		}
	}
	c.saveFramebuffer(false)
	return nil
}

// SetHWAnimation triggers a hardware-driven animation effect.
func (c *ITE8291) SetHWAnimation(animID, speed int) error {
	if speed < 0 {
		speed = 0
	}
	if speed > 10 {
		speed = 10
	}
	c.userMode = false
	return c.sendCtrl(cmd8291SetEffect, ctrl8291Apply, byte(animID),
		byte(speed), c.hwBright, 0x00, 0x00, 0x00)
}

// SetPaletteColor sets one of the seven hardware palette slots (1-7) used by
// the built-in animations.
func (c *ITE8291) SetPaletteColor(idx int, r, g, b byte) error {
	if idx < 1 || idx > 7 {
		return fmt.Errorf("palette index must be 1-7, got %d", idx)
	}
	return c.sendCtrl(cmd8291SetPalette, 0x00, byte(idx), r, g, b)
}

// Off turns off all keyboard LEDs.
func (c *ITE8291) Off() error {
	c.userMode = false
	return c.sendCtrl(cmd8291SetEffect, ctrl8291Off)
}

// GetFirmwareInfo reads the controller firmware version.
func (c *ITE8291) GetFirmwareInfo() ([]byte, error) {
	if err := c.sendCtrl(cmd8291GetFirmware); err != nil {
		return nil, err
	}
	return c.getCtrl()
}

// KeymapID names this controller's calibrated keymap file.
func (c *ITE8291) KeymapID() string { return KeymapIDITE8291 }

// DefaultKeymap returns the built-in key map, which is empty until calibrated.
func (c *ITE8291) DefaultKeymap() map[string][2]int { return DefaultMap8291 }
