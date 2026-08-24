package lightbar

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// The golden packets of the chassis lightbar, byte for byte, per product.
//
// This is the test the repository's own header calls its most expensive bug:
// the variant byte that follows each command is the ONE field that has to match
// the exact MCU, a sibling's value is silently read as a different command, and
// sending 0x21 to this machine's 0x7001 left the bar parked in a mode that a
// power cycle did not undo. Until now nothing read those bytes back — the
// packets are built in memory and handed to an ioctl — so a one-character edit
// to variant8233 shipped with the whole suite green.
//
// Nothing here reaches a device: the capture hook stands in for the kernel and
// the descriptor is a file in t.TempDir(). The device is still really opened,
// so the "is not open" guards stay on the path under test.

// captured records every buffer a controller puts on the wire, in order.
type captured struct{ packets [][]byte }

// openCapturing returns a controller for one product whose transfers are
// recorded instead of sent. The descriptor is a temp file, never /dev/hidraw.
func openCapturing(t *testing.T, product uint16) (*ITE8233, *captured) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hidraw-stub")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating the stand-in descriptor: %v", err)
	}
	dev := &hidraw.HidrawDevice{Path: path}

	log := &captured{}
	previous := hidraw.CaptureForTests
	hidraw.CaptureForTests = func(_ *hidraw.HidrawDevice, _ hidraw.ReportKind, buf []byte) error {
		log.packets = append(log.packets, append([]byte(nil), buf...))
		return nil
	}
	t.Cleanup(func() { hidraw.CaptureForTests = previous })

	ctrl := NewITE8233(dev, product)
	if err := ctrl.Open(); err != nil {
		t.Fatalf("opening the stand-in descriptor: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })
	return ctrl, log
}

func (c *captured) only(t *testing.T) []byte {
	t.Helper()
	if len(c.packets) != 1 {
		t.Fatalf("expected exactly one packet, got %d: %s", len(c.packets), c)
	}
	return c.packets[0]
}

func (c *captured) String() string {
	out := ""
	for _, p := range c.packets {
		out += "\n  " + hex.EncodeToString(p)
	}
	return out
}

func wantBytes(t *testing.T, what string, got []byte, want ...byte) {
	t.Helper()
	if len(got) != 9 {
		t.Errorf("%s: the wire buffer is %d bytes, want 9 (report id + 8)", what, len(got))
		return
	}
	if string(got) != string(want) {
		t.Errorf("%s:\n got %s\nwant %s", what, hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// One colour packet and one mode packet per product, written out in full. The
// three products differ ONLY in the variant byte at index 2, which is exactly
// what makes a wrong one so easy to miss by eye.
func TestGoldenPacketsPerProduct(t *testing.T) {
	for _, tc := range []struct {
		product uint16
		colour  []byte // SetColorSlot(3, 0xAA, 0xBB, 0xCC)
		mode    []byte // setMode(static, 50, 1)
	}{
		{
			product: 0x7001, // the Avell Storm 470's bar, measured on this machine
			colour:  []byte{0x00, 0x14, 0x00, 0x03, 0xAA, 0xBB, 0xCC, 0x00, 0x00},
			mode:    []byte{0x00, 0x08, 0x22, 0x01, 0x01, 0x32, 0x01, 0x00, 0x00},
		},
		// INHERITED, NOT MEASURED. The two rows below come from what
		// tuxedo-drivers claims, exactly as the PIDs8233 comment in
		// ite8233.go says ("Only 0x7001 is verified here"). Neither bar
		// exists on this machine. If someone ever corrects one of these
		// from a datasheet or a USB capture, this test going red is the
		// CORRECTION landing, not a regression — update the row and say
		// where the new value came from.
		{
			product: 0x7000,
			colour:  []byte{0x00, 0x14, 0x01, 0x03, 0xAA, 0xBB, 0xCC, 0x00, 0x00},
			mode:    []byte{0x00, 0x08, 0x21, 0x01, 0x01, 0x32, 0x01, 0x00, 0x00},
		},
		{
			product: 0x6010,
			colour:  []byte{0x00, 0x14, 0x00, 0x03, 0xAA, 0xBB, 0xCC, 0x00, 0x00},
			mode:    []byte{0x00, 0x08, 0x02, 0x01, 0x01, 0x32, 0x01, 0x00, 0x00},
		},
	} {
		ctrl, log := openCapturing(t, tc.product)

		if err := ctrl.SetColorSlot(3, 0xAA, 0xBB, 0xCC); err != nil {
			t.Fatalf("%04x SetColorSlot: %v", tc.product, err)
		}
		wantBytes(t, hex.EncodeToString([]byte{byte(tc.product >> 8), byte(tc.product)})+" colour slot",
			log.only(t), tc.colour...)

		log.packets = nil
		if err := ctrl.setMode(Effects8233["static"], 50, MinSpeed8233); err != nil {
			t.Fatalf("%04x setMode: %v", tc.product, err)
		}
		wantBytes(t, hex.EncodeToString([]byte{byte(tc.product >> 8), byte(tc.product)})+" mode",
			log.only(t), tc.mode...)
	}
}

// The variant byte read straight off the wire, per product and per command. The
// table in variant8233 tracks colour and mode separately because 0x7000
// disagrees with itself; a test that only looked at one command would let the
// other half rot.
func TestTheVariantByteOnTheWireMatchesTheProduct(t *testing.T) {
	//
	// Provenance, because these are not equal in confidence: 0x7001 is
	// MEASURED on this machine (0x22 is the byte that makes the Storm 470's
	// bar answer at all); 0x7000 and 0x6010 are INHERITED from
	// tuxedo-drivers and no one here has seen either device.
	for product, want := range map[uint16][2]byte{
		0x6010: {0x00, 0x02},
		0x7000: {0x01, 0x21},
		0x7001: {0x00, 0x22},
	} {
		ctrl, log := openCapturing(t, product)

		if err := ctrl.SetColorSlot(1, 0x01, 0x02, 0x03); err != nil {
			t.Fatalf("%04x SetColorSlot: %v", product, err)
		}
		if got := log.only(t)[2]; got != want[0] {
			t.Errorf("%04x colour command carried variant %#02x, want %#02x", product, got, want[0])
		}

		log.packets = nil
		if err := ctrl.setMode(Effects8233["wave"], 10, 5); err != nil {
			t.Fatalf("%04x setMode: %v", product, err)
		}
		if got := log.only(t)[2]; got != want[1] {
			t.Errorf("%04x mode command carried variant %#02x, want %#02x", product, got, want[1])
		}
	}
}

// No two products may share a mode variant. This is the property that turns the
// table into a safety net: as long as the values are distinct, a copy-paste
// between rows shows up in the test above instead of on the hardware.
func TestTheModeVariantsAreDistinctPerProduct(t *testing.T) {
	seen := map[byte]uint16{}
	for product, pair := range variant8233 {
		if other, taken := seen[pair[1]]; taken {
			t.Errorf("products %04x and %04x share mode variant %#02x — one of them is a copy-paste",
				other, product, pair[1])
		}
		seen[pair[1]] = product
	}
}

// SetColor is the whole call the CLI and the pulse daemon use: slot 1 first,
// then static mode. The order is load-bearing — a mode command that arrives
// before the colour list runs the animation over the OLD colours — and so is
// the fact that brightness travels in the mode packet, not the colour one.
func TestSetColorEmitsTheColourThenTheMode(t *testing.T) {
	ctrl, log := openCapturing(t, 0x7001)

	if err := ctrl.SetColor(0x10, 0x20, 0x30, 100); err != nil {
		t.Fatalf("SetColor: %v", err)
	}
	if len(log.packets) != 2 {
		t.Fatalf("SetColor sent %d packets, want 2: %s", len(log.packets), log)
	}
	wantBytes(t, "SetColor colour", log.packets[0],
		0x00, 0x14, 0x00, 0x01, 0x10, 0x20, 0x30, 0x00, 0x00)
	wantBytes(t, "SetColor mode", log.packets[1],
		0x00, 0x08, 0x22, 0x01, 0x01, 0x64, 0x01, 0x00, 0x00)
}

// SetEffect writes the seven colour slots and only then starts the animation,
// with the per-mode trailing "apply" byte the vendor driver uses.
func TestSetEffectFillsTheSevenSlotsBeforeStartingTheAnimation(t *testing.T) {
	ctrl, log := openCapturing(t, 0x7001)

	if err := ctrl.SetEffect("breathing", Rainbow8233, 100, MaxSpeed8233); err != nil {
		t.Fatalf("SetEffect: %v", err)
	}
	if len(log.packets) != ColorSlots8233+1 {
		t.Fatalf("SetEffect sent %d packets, want %d: %s", len(log.packets), ColorSlots8233+1, log)
	}
	for i, colour := range Rainbow8233 {
		wantBytes(t, "SetEffect slot", log.packets[i],
			0x00, 0x14, 0x00, byte(i+1), colour[0], colour[1], colour[2], 0x00, 0x00)
	}
	// mode 0x02 (breathing), speed 0x0A, brightness 0x64, apply 0x08.
	wantBytes(t, "SetEffect mode", log.packets[ColorSlots8233],
		0x00, 0x08, 0x22, 0x02, 0x0A, 0x64, 0x08, 0x00, 0x00)
}

// Off is static black, and it must never look like the vendor's power-off
// stage: 0x08 0x01 on this MCU is the sequence the header warns about, the one
// that survives a reboot. With variant 0x22 in place the mode packet starts
// 0x08 0x22, which is why the byte is worth a test of its own.
func TestOffIsStaticBlackAndNotThePowerOffStage(t *testing.T) {
	ctrl, log := openCapturing(t, 0x7001)

	if err := ctrl.Off(); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if len(log.packets) != 2 {
		t.Fatalf("Off sent %d packets, want 2: %s", len(log.packets), log)
	}
	wantBytes(t, "Off colour", log.packets[0],
		0x00, 0x14, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00)
	wantBytes(t, "Off mode", log.packets[1],
		0x00, 0x08, 0x22, 0x01, 0x01, 0x00, 0x01, 0x00, 0x00)
	for _, p := range log.packets {
		if p[1] == cmd8233SetMode && p[2] == 0x01 {
			t.Errorf("Off emitted the power-off stage 0x08 0x01: %s", hex.EncodeToString(p))
		}
	}
}

// Brightness and speed reach the wire unscaled, and out-of-range values are
// refused before anything is sent rather than clamped into a different packet.
func TestBrightnessAndSpeedAreRefusedOutOfRangeAndSentVerbatim(t *testing.T) {
	ctrl, log := openCapturing(t, 0x7001)

	if err := ctrl.setMode(Effects8233["static"], MaxBrightness8233+1, MinSpeed8233); err == nil {
		t.Error("a brightness above the wire maximum was accepted")
	}
	if err := ctrl.setMode(Effects8233["static"], 0, MaxSpeed8233+1); err == nil {
		t.Error("a speed above the wire maximum was accepted")
	}
	if len(log.packets) != 0 {
		t.Fatalf("a refused mode still put %d packets on the wire: %s", len(log.packets), log)
	}

	if err := ctrl.setMode(Effects8233["scan"], 0x37, 0x09); err != nil {
		t.Fatalf("setMode: %v", err)
	}
	wantBytes(t, "verbatim brightness and speed", log.only(t),
		0x00, 0x08, 0x22, 0x06, 0x09, 0x37, 0x01, 0x00, 0x00)
}
