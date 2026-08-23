package lightbar

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// ITE 8233 chassis lightbar, a second MCU that sits beside the ITE 8291
// keyboard controller on Uniwill/TongFang barebones such as the Avell Storm
// 470. It has nothing to do with the Clevo ITE 8911 in ite8911.go: colours are
// true RGB triples rather than a fixed palette of colour IDs, and the whole
// bar is one zone.
//
// The packet layout comes from tuxedo-drivers src/ite_8291_lb/ite_8291_lb.c,
// which names three sibling product IDs and gives each its own variant byte.
// Sending another sibling's variant byte does not fail — it is quietly
// interpreted as a different command, which is how a stray 0x08 0x01 (the
// 0x6010 power-off stage) can leave this bar dark with every write still
// returning success.
const (
	VID8233 = 0x048D

	// The vendor interface declares an 8-byte feature report with no report
	// ID, so every hidraw buffer carries a leading zero byte that the kernel
	// strips before the packet reaches the wire.
	ctrlLen8233 = 1 + 8

	cmd8233SetColor = 0x14
	cmd8233SetMode  = 0x08

	// Brightness is 0x00-0x64 on the wire; avellcc exposes 0-100 unchanged.
	MaxBrightness8233 = 0x64
	// Speed runs from 0x01 (fastest) to 0x0A (slowest).
	MinSpeed8233 = 0x01
	MaxSpeed8233 = 0x0A

	// The animated modes cycle through a list of seven colour slots.
	ColorSlots8233 = 7
)

// PIDs8233 lists the product IDs that speak this protocol. Only 0x7001 is
// verified here; the other two are what tuxedo-drivers claims.
var PIDs8233 = []uint16{0x6010, 0x7000, 0x7001}

// variant8233 gives the per-product byte that follows each command. It is the
// one field that must match the exact MCU: the colour command and the mode
// command disagree on it for 0x7000, so they are tracked separately.
var variant8233 = map[uint16][2]byte{
	// product: {colour variant, mode variant}
	0x6010: {0x00, 0x02},
	0x7000: {0x01, 0x21},
	0x7001: {0x00, 0x22},
}

// Effects8233 maps effect names to the controller's built-in animations.
// "static" is the direct mode that hands colour control to the host; it also
// cancels whichever animation is running.
var Effects8233 = map[string]byte{
	"static":    0x01,
	"breathing": 0x02,
	"wave":      0x03,
	"bounce":    0x04,
	"marquee":   0x05,
	"scan":      0x06,
}

// apply8233 is the trailing byte each mode needs. tuxedo-drivers uses 0x08 for
// the modes that read the colour list and 0x01 for the ones that generate
// their own colours; both were confirmed on the Storm 470's 0x7001.
var apply8233 = map[byte]byte{
	0x01: 0x01,
	0x02: 0x08,
	0x03: 0x01,
	0x04: 0x08,
	0x05: 0x01,
	0x06: 0x01,
}

// Rainbow8233 is the seven-slot colour list the animated modes cycle through
// when the caller does not supply one. It reproduces the factory rainbow.
var Rainbow8233 = [ColorSlots8233][3]byte{
	{0xFF, 0x00, 0x00}, {0xFF, 0xFF, 0x00}, {0x00, 0xFF, 0x00}, {0x00, 0xFF, 0xFF},
	{0x00, 0x00, 0xFF}, {0xFF, 0x00, 0xFF}, {0xFF, 0xFF, 0xFF},
}

// vendorPagePrefix8233 identifies the vendor collection (usage page 0xFF03)
// that carries the lightbar protocol.
var vendorPagePrefix8233 = []byte{0x06, 0x03, 0xFF}

// EffectNames8233 returns the supported effect names in stable order.
func EffectNames8233() []string {
	names := make([]string, 0, len(Effects8233))
	for name := range Effects8233 {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ITE8233 drives the ITE 8233 chassis lightbar.
type ITE8233 struct {
	dev     *hidraw.HidrawDevice
	ownsDev bool
	product uint16
}

// NewITE8233 creates a controller. If dev is nil it auto-discovers, which is
// also what resolves the product ID; pass a device only when the caller
// already knows both.
func NewITE8233(dev *hidraw.HidrawDevice, product uint16) *ITE8233 {
	return &ITE8233{dev: dev, ownsDev: dev == nil, product: product}
}

// Name identifies the controller in user-facing output.
func (c *ITE8233) Name() string { return "ITE 8233" }

// Path returns the device path, empty before Open.
func (c *ITE8233) Path() string {
	if c.dev != nil {
		return c.dev.Path
	}
	return ""
}

// Product returns the resolved USB product ID.
func (c *ITE8233) Product() uint16 { return c.product }

// FindITE8233 locates the lightbar's vendor HID interface and its product ID.
// The device also exposes a plain keyboard interface, which must not be
// written to, so the report descriptor decides rather than the product ID.
func FindITE8233() (string, uint16, error) {
	for _, pid := range PIDs8233 {
		paths, err := hidraw.FindHidrawAll(VID8233, pid)
		if err != nil {
			return "", 0, err
		}
		for _, path := range paths {
			desc, err := hidraw.ReportDescriptor(path)
			if err != nil {
				continue
			}
			if bytes.HasPrefix(desc, vendorPagePrefix8233) {
				return path, pid, nil
			}
		}
	}
	return "", 0, fmt.Errorf("no ITE 8233 chassis lightbar found (vendor %04x, products %04x/%04x/%04x)",
		VID8233, PIDs8233[0], PIDs8233[1], PIDs8233[2])
}

// findITE8233 is the discovery hook. It is a variable only so a test can drive
// the failed-reopen path — the one that used to leave a nil device behind —
// without a lightbar attached. Production always calls FindITE8233.
var findITE8233 = FindITE8233

// Open opens the hidraw device, auto-discovering it if needed.
func (c *ITE8233) Open() error {
	if c.dev == nil {
		path, pid, err := findITE8233()
		if err != nil {
			return err
		}
		c.dev = &hidraw.HidrawDevice{Path: path}
		c.ownsDev = true
		c.product = pid
	}
	if _, ok := variant8233[c.product]; !ok {
		return fmt.Errorf("ITE 8233 product %04x has no known command variant", c.product)
	}
	return c.dev.Open()
}

// Reopen closes the device and discovers it again.
//
// A USB re-enumeration — which a suspend/resume cycle can cause — leaves the
// old descriptor valid as a file and dead as a device: every ioctl fails with
// ENODEV. A daemon that opens the device once at start has no way back from
// that on its own, so it has to be able to ask for a fresh one.
//
// Discovery runs before the device is replaced. Clearing c.dev first meant that
// the case Reopen exists for — the device has not come back yet — left dev nil
// for good, and the next write in the pulse loop panicked on a nil pointer
// instead of returning the error the backoff was written to handle. The old
// descriptor is closed either way, so a send after a failed Reopen fails
// cleanly rather than writing to a stale fd.
func (c *ITE8233) Reopen() error {
	if c.dev != nil {
		_ = c.dev.Close()
	}
	path, pid, err := findITE8233()
	if err != nil {
		return err
	}
	c.dev = &hidraw.HidrawDevice{Path: path}
	c.ownsDev = true
	c.product = pid
	return c.Open()
}

// Close releases the device if this controller owns it.
func (c *ITE8233) Close() error {
	if c.dev != nil && c.ownsDev {
		return c.dev.Close()
	}
	return nil
}

// send writes one 8-byte control packet as a feature report.
//
// The nil check is what keeps a daemon alive: a controller that was never
// opened, or whose Reopen found no device, has no descriptor, and the pulse
// loop calls SetColor on every frame. An error is something the backoff can
// handle; a nil dereference takes the whole unit down.
func (c *ITE8233) send(payload [8]byte) error {
	if c.dev == nil {
		return fmt.Errorf("ITE 8233 lightbar is not open")
	}
	buf := make([]byte, ctrlLen8233)
	copy(buf[1:], payload[:])
	return c.dev.SendFeatureReport(buf)
}

// SendRaw writes one control packet verbatim, for protocol work. Nothing
// validates it: this controller accepts a wrong variant byte as a different
// command and still reports success.
func (c *ITE8233) SendRaw(payload [8]byte) error {
	return c.send(payload)
}

// SetColorSlot writes one entry of the seven-slot colour list the animated
// modes cycle through. Slots are 1-based; slot 1 doubles as the colour used by
// the static mode.
func (c *ITE8233) SetColorSlot(slot int, r, g, b byte) error {
	if slot < 1 || slot > ColorSlots8233 {
		return fmt.Errorf("colour slot range is 1-%d, got %d", ColorSlots8233, slot)
	}
	return c.send([8]byte{cmd8233SetColor, variant8233[c.product][0], byte(slot), r, g, b, 0x00, 0x00})
}

// SetColorList writes the whole colour list in one pass.
func (c *ITE8233) SetColorList(colors [ColorSlots8233][3]byte) error {
	for i, color := range colors {
		if err := c.SetColorSlot(i+1, color[0], color[1], color[2]); err != nil {
			return err
		}
	}
	return nil
}

// setMode selects a built-in mode. brightness is 0-100, speed 1-10.
func (c *ITE8233) setMode(mode byte, brightness, speed int) error {
	if brightness < 0 || brightness > MaxBrightness8233 {
		return fmt.Errorf("brightness range is 0-%d, got %d", MaxBrightness8233, brightness)
	}
	if speed < MinSpeed8233 || speed > MaxSpeed8233 {
		return fmt.Errorf("speed range is %d-%d, got %d", MinSpeed8233, MaxSpeed8233, speed)
	}
	apply, ok := apply8233[mode]
	if !ok {
		return fmt.Errorf("unknown lightbar mode %#02x", mode)
	}
	return c.send([8]byte{cmd8233SetMode, variant8233[c.product][1], mode,
		byte(speed), byte(brightness), apply, 0x00, 0x00})
}

// SetColor lights the whole bar one colour at the given brightness (0-100),
// switching it into static mode. This is what cancels a running animation.
func (c *ITE8233) SetColor(r, g, b byte, brightness int) error {
	if err := c.SetColorSlot(1, r, g, b); err != nil {
		return err
	}
	return c.setMode(Effects8233["static"], brightness, MinSpeed8233)
}

// SetEffect starts a built-in animation over the given colour list. brightness
// is 0-100 and speed 1-10, fastest first.
func (c *ITE8233) SetEffect(name string, colors [ColorSlots8233][3]byte, brightness, speed int) error {
	mode, ok := Effects8233[name]
	if !ok {
		return fmt.Errorf("unknown lightbar effect %q (have %v)", name, EffectNames8233())
	}
	if err := c.SetColorList(colors); err != nil {
		return err
	}
	return c.setMode(mode, brightness, speed)
}

// Off darkens the bar by taking it to static black at zero brightness.
//
// It deliberately does not send the vendor's four-stage power-off sequence.
// That sequence ends in a state this MCU only leaves on a mode command carrying
// the right variant byte — recoverable, but not by a power cycle, and every
// write in between still reports success. Static black is reversible by any
// later colour write, which is the property that matters for a CLI.
func (c *ITE8233) Off() error {
	return c.SetColor(0, 0, 0, 0)
}
