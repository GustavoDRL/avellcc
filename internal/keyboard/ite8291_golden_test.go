package keyboard

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// The golden packets of the ITE 8291 rev 3 keyboard controller, byte for byte,
// plus the brightness round trip that used to lose up to four hardware steps
// every time the device was opened.
//
// Nothing here reaches a device or the real framebuffer mirror: the descriptor
// is a file in t.TempDir(), the capture hook stands in for the kernel, and
// XDG_CONFIG_HOME is redirected so ~/.config/avellcc is never written.

type transfer struct {
	kind hidraw.ReportKind
	buf  []byte
}

type keyboardWire struct {
	sent  []transfer
	reply []byte // what a GET_FEATURE hands back, 9 bytes
}

// openCapturingKeyboard opens a controller whose transfers are recorded. reply
// is the 8-byte control payload the controller is pretending to answer with
// ([echo, control, effect, speed, brightness, ...]), which is what readState
// parses during Open.
func openCapturingKeyboard(t *testing.T, reply [8]byte) (*ITE8291, *keyboardWire) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), "hidraw-stub")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating the stand-in descriptor: %v", err)
	}

	wire := &keyboardWire{reply: append([]byte{0x00}, reply[:]...)}
	previous := hidraw.CaptureForTests
	hidraw.CaptureForTests = func(_ *hidraw.HidrawDevice, kind hidraw.ReportKind, buf []byte) error {
		wire.sent = append(wire.sent, transfer{kind, append([]byte(nil), buf...)})
		if kind == hidraw.ReportGetFeature {
			copy(buf, wire.reply)
		}
		return nil
	}
	t.Cleanup(func() { hidraw.CaptureForTests = previous })

	ctrl := NewITE8291(&hidraw.HidrawDevice{Path: path})
	if err := ctrl.Open(); err != nil {
		t.Fatalf("opening the stand-in descriptor: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })
	wire.sent = nil // drop the readState exchange; each test asks for its own
	return ctrl, wire
}

func (w *keyboardWire) String() string {
	out := ""
	for _, tr := range w.sent {
		out += "\n  " + string(tr.kind) + " " + hex.EncodeToString(tr.buf)
	}
	return out
}

func (w *keyboardWire) onlyControl(t *testing.T) []byte {
	t.Helper()
	if len(w.sent) != 1 || w.sent[0].kind != hidraw.ReportSetFeature {
		t.Fatalf("expected exactly one control packet, got:%s", w)
	}
	return w.sent[0].buf
}

func wantCtrl(t *testing.T, what string, got []byte, want ...byte) {
	t.Helper()
	if len(got) != ctrlLen8291 {
		t.Errorf("%s: the wire buffer is %d bytes, want %d (report id + 8)", what, len(got), ctrlLen8291)
		return
	}
	if string(got) != string(want) {
		t.Errorf("%s:\n got %s\nwant %s", what, hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// The three commands the CLI sends most, written out in full. The leading 0x00
// is the report ID the kernel strips; the vendor interface has none.
func TestGoldenControlPackets(t *testing.T) {
	// A reply that says: off, no user mode, hardware brightness 50.
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Off, 0x00, 0x00, 50, 0, 0, 0})

	if err := ctrl.SetBrightness(7); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	// 0x09 set-brightness, 0x02 apply, 7 * 50 / 10 = 35 = 0x23.
	wantCtrl(t, "SetBrightness(7)", wire.onlyControl(t),
		0x00, 0x09, 0x02, 0x23, 0x00, 0x00, 0x00, 0x00, 0x00)

	wire.sent = nil
	if err := ctrl.SetHWAnimation(HWEffects8291["rainbow"], 4); err != nil {
		t.Fatalf("SetHWAnimation: %v", err)
	}
	// 0x08 set-effect, 0x02 apply, 0x05 rainbow, speed 4, brightness 35.
	wantCtrl(t, "SetHWAnimation(rainbow, 4)", wire.onlyControl(t),
		0x00, 0x08, 0x02, 0x05, 0x04, 0x23, 0x00, 0x00, 0x00)

	wire.sent = nil
	if err := ctrl.SetPaletteColor(2, 0xAA, 0xBB, 0xCC); err != nil {
		t.Fatalf("SetPaletteColor: %v", err)
	}
	// 0x14 set-palette, a zero sub-command byte, slot 2, r, g, b.
	wantCtrl(t, "SetPaletteColor(2)", wire.onlyControl(t),
		0x00, 0x14, 0x00, 0x02, 0xAA, 0xBB, 0xCC, 0x00, 0x00)

	wire.sent = nil
	if err := ctrl.Off(); err != nil {
		t.Fatalf("Off: %v", err)
	}
	// 0x08 set-effect with the OFF control byte and nothing else: the rest of
	// the packet stays zero, which is what makes it reversible.
	wantCtrl(t, "Off", wire.onlyControl(t),
		0x00, 0x08, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
}

// A row goes out as a 65-byte OUTPUT report — a different transfer from the
// control packets — preceded by the control packet that selects the row. The
// wire order inside the report is all blues, then all greens, then all reds,
// which is the kind of thing that is invisible until the keyboard turns the
// wrong colour.
func TestGoldenRowReport(t *testing.T) {
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Apply, effect8291UserMode, 0x00, 50, 0, 0, 0})

	if err := ctrl.SetKeyColor(2, 5, 0x11, 0x22, 0x33); err != nil {
		t.Fatalf("SetKeyColor: %v", err)
	}
	// readState saw user mode already on, so no repaint: one row-index packet
	// and one row report.
	if len(wire.sent) != 2 {
		t.Fatalf("SetKeyColor sent %d transfers, want 2:%s", len(wire.sent), wire)
	}
	wantCtrl(t, "row index", wire.sent[0].buf,
		0x00, 0x16, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00)

	row := wire.sent[1]
	if row.kind != hidraw.ReportOutput {
		t.Errorf("the row went out as %q, want an output report", row.kind)
	}
	if len(row.buf) != rowLen8291 {
		t.Fatalf("the row report is %d bytes, want %d", len(row.buf), rowLen8291)
	}
	want := make([]byte, rowLen8291)
	want[1+0*Cols8291+5] = 0x33 // blue plane
	want[1+1*Cols8291+5] = 0x22 // green plane
	want[1+2*Cols8291+5] = 0x11 // red plane
	if string(row.buf) != string(want) {
		t.Errorf("row report:\n got %s\nwant %s",
			hex.EncodeToString(row.buf), hex.EncodeToString(want))
	}
}

// The whole 0-10 scale, so the conversion cannot be right at the ends and wrong
// in the middle.
func TestEveryBrightnessLevelMapsToItsHardwareStep(t *testing.T) {
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Off, 0x00, 0x00, 50, 0, 0, 0})

	for level := 0; level <= MaxBrightness; level++ {
		wire.sent = nil
		if err := ctrl.SetBrightness(level); err != nil {
			t.Fatalf("SetBrightness(%d): %v", level, err)
		}
		got := wire.onlyControl(t)[3]
		if want := byte(level * 5); got != want {
			t.Errorf("SetBrightness(%d) put %#02x on the wire, want %#02x", level, got, want)
		}
	}
}

// THE ROUND TRIP. readState reads the controller's own 0-50 brightness; every
// later command has to send back the value that was read. It used to be
// quantised to the 0-10 CLI scale on the way in (48 -> 9) and expanded again on
// the way out (9 -> 45), so every hook and every resume that had to re-enter
// user mode darkened the keyboard by up to four steps, cumulatively, until it
// settled on a multiple of five. Nobody asked for that.
func TestABrightnessReadFromTheControllerIsSentBackUnchanged(t *testing.T) {
	for _, hw := range []byte{0, 1, 7, 23, 44, 47, 48, 49, 50} {
		// Not in user mode, so the next colour write has to re-enter it and
		// that is the packet carrying the brightness back.
		ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Apply, 0x02, 0x00, hw, 0, 0, 0})

		if err := ctrl.SetAllKeys(0x01, 0x02, 0x03); err != nil {
			t.Fatalf("hw=%d SetAllKeys: %v", hw, err)
		}
		if len(wire.sent) == 0 {
			t.Fatalf("hw=%d: nothing was sent", hw)
		}
		enter := wire.sent[0].buf
		wantCtrl(t, "enter user mode", enter,
			0x00, 0x08, 0x02, 0x33, 0x00, hw, 0x00, 0x00, 0x00)
		if enter[5] != hw {
			t.Errorf("the controller reported brightness %d and got %d back — %d hardware steps lost",
				hw, enter[5], int(hw)-int(enter[5]))
		}
	}
}

// A hardware animation started right after Open must carry the same value, for
// the same reason: it is the other command that re-sends brightness without the
// user having asked for a brightness change.
func TestAHardwareAnimationKeepsTheBrightnessThatWasRead(t *testing.T) {
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Apply, 0x02, 0x00, 48, 0, 0, 0})

	if err := ctrl.SetHWAnimation(HWEffects8291["wave"], 3); err != nil {
		t.Fatalf("SetHWAnimation: %v", err)
	}
	wantCtrl(t, "SetHWAnimation after a 48 read", wire.onlyControl(t),
		0x00, 0x08, 0x02, 0x03, 0x03, 48, 0x00, 0x00, 0x00)
}

// An explicit brightness from the CLI still wins over what was read — fixing
// the read must not break the write.
func TestAnExplicitBrightnessOverridesTheOneThatWasRead(t *testing.T) {
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Apply, 0x02, 0x00, 48, 0, 0, 0})

	if err := ctrl.SetBrightness(4); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	wire.sent = nil
	if err := ctrl.SetAllKeys(0x01, 0x02, 0x03); err != nil {
		t.Fatalf("SetAllKeys: %v", err)
	}
	if got := wire.sent[0].buf[5]; got != 20 {
		t.Errorf("re-entering user mode after SetBrightness(4) sent %d, want 20", got)
	}
}

// readState also decides whether user mode is already on, which is what tells
// SetKeyColor it can send one row instead of repainting the whole grid.
func TestReadStateRecognisesUserMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		control  byte
		effect   byte
		repaints bool
	}{
		{"already in user mode", ctrl8291Apply, effect8291UserMode, false},
		{"running an animation", ctrl8291Apply, 0x05, true},
		{"switched off", ctrl8291Off, effect8291UserMode, true},
	} {
		ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, tc.control, tc.effect, 0x00, 50, 0, 0, 0})

		if err := ctrl.SetKeyColor(0, 0, 1, 2, 3); err != nil {
			t.Fatalf("%s: SetKeyColor: %v", tc.name, err)
		}
		rows := 0
		for _, tr := range wire.sent {
			if tr.kind == hidraw.ReportOutput {
				rows++
			}
		}
		want := 1
		if tc.repaints {
			want = Rows8291
		}
		if rows != want {
			t.Errorf("%s: %d row reports went out, want %d — %s", tc.name, rows, want,
				map[bool]string{true: "the controller's buffer was discarded and the grid needs a full repaint",
					false: "user mode was already on, so one row is enough"}[tc.repaints])
		}
	}
}

// A brightness byte outside the controller's range is a garbled reply, and the
// last known value has to survive it rather than being replaced by nonsense.
func TestAnImpossibleBrightnessInTheReplyIsIgnored(t *testing.T) {
	ctrl, wire := openCapturingKeyboard(t, [8]byte{0x88, ctrl8291Apply, 0x02, 0x00, 0xFF, 0, 0, 0})

	if err := ctrl.SetAllKeys(1, 2, 3); err != nil {
		t.Fatalf("SetAllKeys: %v", err)
	}
	if got := wire.sent[0].buf[5]; got != hwBrightness(MaxBrightness) {
		t.Errorf("a reply with brightness 0xFF left %d on the wire, want the default %d",
			got, hwBrightness(MaxBrightness))
	}
}
