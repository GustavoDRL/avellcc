package lightbar

import (
	"strings"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
)

// Regressions for the crash the audit reproduced in the pulse daemon: a write
// on a controller with no descriptor. None of these touches a real device —
// discovery is stubbed and the paths do not exist — because a test that writes
// to /dev/hidraw would drive the machine's own lightbar.

// stubDiscovery replaces the discovery hook for one test.
func stubDiscovery(t *testing.T, path string, product uint16, err error) {
	t.Helper()
	previous := findITE8233
	findITE8233 = func() (string, uint16, error) { return path, product, err }
	t.Cleanup(func() { findITE8233 = previous })
}

// The daemon calls SetColor on every frame. Before the guard, a controller that
// had never been opened dereferenced a nil device and took the unit down with
// it; the backoff around the call can only work if it gets an error.
func TestSetColorOnAControllerThatWasNeverOpenedReturnsAnError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SetColor panicked instead of returning an error: %v", r)
		}
	}()

	err := NewITE8233(nil, 0x7001).SetColor(0xFF, 0x00, 0x00, 50)
	if err == nil {
		t.Fatal("SetColor on a controller that was never opened returned no error")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("the error does not say the bar is not open: %v", err)
	}
}

// The exact sequence from the journal: a write fails after a USB
// re-enumeration, Reopen cannot find the device yet, the backoff waits, and the
// next frame writes again. Reopen used to clear the device before discovery, so
// that next write was a nil dereference.
func TestAFailedReopenLeavesAControllerThatErrorsInsteadOfPanicking(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the write after a failed Reopen panicked: %v", r)
		}
	}()

	ctrl := NewITE8233(&hidraw.HidrawDevice{Path: "/nonexistent/hidraw-test"}, 0x7001)
	stubDiscovery(t, "", 0, errNoDevice{})

	if err := ctrl.Reopen(); err == nil {
		t.Fatal("Reopen reported success although discovery failed")
	}
	// Discovery runs before the device is replaced, so a failed Reopen still
	// knows which device it had — the path the daemon prints in its errors.
	if got := ctrl.Path(); got != "/nonexistent/hidraw-test" {
		t.Errorf("a failed Reopen forgot the device it had: Path() = %q", got)
	}
	err := ctrl.SetColor(0x00, 0xFF, 0x00, 50)
	if err == nil {
		t.Fatal("the write after a failed Reopen returned no error")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("the error does not say the bar is not open: %v", err)
	}
}

// The other half of the same fix: when the device does come back, Reopen has to
// pick up the new path and product.
func TestReopenAdoptsTheRediscoveredDevice(t *testing.T) {
	ctrl := NewITE8233(&hidraw.HidrawDevice{Path: "/nonexistent/hidraw-old"}, 0x7001)
	stubDiscovery(t, "/nonexistent/hidraw-new", 0x7000, nil)

	// Open still fails — the path does not exist — but the controller must be
	// pointing at what discovery returned, not at the dead descriptor.
	_ = ctrl.Reopen()
	if got := ctrl.Path(); got != "/nonexistent/hidraw-new" {
		t.Errorf("Path() = %q, want the rediscovered path", got)
	}
	if got := ctrl.Product(); got != 0x7000 {
		t.Errorf("Product() = %#04x, want 0x7000", got)
	}
}

// A device that failed to open is as unusable as one that was never assigned,
// and the guard in the hidraw layer is what turns that into an error for every
// controller in the repo rather than an ioctl on whatever fd 0 happens to be.
func TestWriteAfterAFailedOpenReturnsAnError(t *testing.T) {
	ctrl := NewITE8233(&hidraw.HidrawDevice{Path: "/nonexistent/hidraw-test"}, 0x7001)
	if err := ctrl.Open(); err == nil {
		t.Fatal("Open on a path that does not exist reported success")
	}
	err := ctrl.SetColor(0x00, 0x00, 0xFF, 50)
	if err == nil {
		t.Fatal("the write after a failed Open returned no error")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("the error does not say the device is not open: %v", err)
	}
}

type errNoDevice struct{}

func (errNoDevice) Error() string { return "no ITE 8233 chassis lightbar found" }
