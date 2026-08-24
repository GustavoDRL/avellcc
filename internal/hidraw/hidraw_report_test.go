package hidraw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The other half of the guard the audit asked for. Refusing a closed device
// stopped the ioctl from landing on descriptor 0; these stop it from taking the
// process down before it gets that far: &buf[0] on an empty slice panics, and
// the pulse daemon calls these on every frame, so a panic here costs the whole
// unit where an error costs one frame.
//
// The descriptor is a regular file in t.TempDir(). Nothing is sent: the guards
// fire before the syscall, and the capture hook replaces it in the one test
// that goes past them.

// openTemp gives a really-open device that is not a controller.
func openTemp(t *testing.T) *HidrawDevice {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hidraw-stub")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating the stand-in descriptor: %v", err)
	}
	d := &HidrawDevice{Path: path}
	if err := d.Open(); err != nil {
		t.Fatalf("opening the stand-in descriptor: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestAnEmptyReportIsRefusedInsteadOfPanicking(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("an empty report panicked instead of returning an error: %v", r)
		}
	}()
	d := openTemp(t)

	if err := d.SendFeatureReport(nil); err == nil {
		t.Error("SendFeatureReport(nil) reported success")
	} else if !strings.Contains(err.Error(), "empty feature report") {
		t.Errorf("SendFeatureReport(nil): %v", err)
	}

	if _, err := d.GetFeatureReport(0x00, 0); err == nil {
		t.Error("GetFeatureReport with length 0 reported success")
	} else if !strings.Contains(err.Error(), "must be positive") {
		t.Errorf("GetFeatureReport(0): %v", err)
	}

	if _, err := d.GetFeatureReport(0x00, -1); err == nil {
		t.Error("GetFeatureReport with a negative length reported success")
	}

	// An empty output report is not a short write: unix.Write returns 0 and
	// the check below it is satisfied by 0 == 0, so the caller would be told
	// the row went out when nothing did.
	if err := d.Write(nil); err == nil {
		t.Error("Write(nil) reported success although nothing went out")
	} else if !strings.Contains(err.Error(), "empty output report") {
		t.Errorf("Write(nil): %v", err)
	}
}

// The capture seam has one property the golden tests depend on: it must see the
// buffer VERBATIM, and it must not weaken the "is not open" guard that sits in
// front of it. A seam that answered before the guard would let a test pass on a
// device the daemon could never have written to.
func TestTheCaptureSeamSeesTheBufferAndDoesNotBypassTheOpenGuard(t *testing.T) {
	var seen []ReportKind
	var payload []byte
	previous := CaptureForTests
	CaptureForTests = func(_ *HidrawDevice, kind ReportKind, buf []byte) error {
		seen = append(seen, kind)
		payload = append([]byte(nil), buf...)
		if kind == ReportGetFeature {
			copy(buf, []byte{0x00, 0xAA, 0xBB})
		}
		return nil
	}
	t.Cleanup(func() { CaptureForTests = previous })

	closed := &HidrawDevice{Path: "/nonexistent/hidraw-test"}
	if err := closed.SendFeatureReport(make([]byte, 9)); err == nil ||
		!strings.Contains(err.Error(), "is not open") {
		t.Fatalf("the seam answered for a device that was never opened: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("the seam was consulted before the open guard: %v", seen)
	}

	d := openTemp(t)
	if err := d.SendFeatureReport([]byte{0x00, 0x14, 0x22}); err != nil {
		t.Fatalf("SendFeatureReport through the seam: %v", err)
	}
	if string(payload) != string([]byte{0x00, 0x14, 0x22}) {
		t.Errorf("the seam saw %v, want the buffer verbatim", payload)
	}

	reply, err := d.GetFeatureReport(0x00, 3)
	if err != nil {
		t.Fatalf("GetFeatureReport through the seam: %v", err)
	}
	if string(reply) != string([]byte{0x00, 0xAA, 0xBB}) {
		t.Errorf("the reply came back as %v, want what the seam wrote", reply)
	}

	if err := d.Write([]byte{0x00, 0x01}); err != nil {
		t.Fatalf("Write through the seam: %v", err)
	}
	if len(seen) != 3 ||
		seen[0] != ReportSetFeature || seen[1] != ReportGetFeature || seen[2] != ReportOutput {
		t.Errorf("the seam recorded the kinds as %v", seen)
	}
}

// With no hook in place the transfers still go to the kernel, which is what
// makes the seam safe to leave in production: a regular file is not a hidraw
// node, so the ioctl fails at the syscall rather than being quietly swallowed.
func TestWithoutTheSeamTheIoctlStillReachesTheKernel(t *testing.T) {
	if CaptureForTests != nil {
		t.Fatal("a previous test left the capture hook installed")
	}
	d := openTemp(t)
	if err := d.SendFeatureReport(make([]byte, 9)); err == nil {
		t.Error("a feature report to a regular file reported success")
	} else if !strings.Contains(err.Error(), "HIDIOCSFEATURE") {
		t.Errorf("the error does not name the ioctl: %v", err)
	}
}
