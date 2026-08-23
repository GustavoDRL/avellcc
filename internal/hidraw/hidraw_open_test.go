package hidraw

import (
	"strings"
	"testing"
)

// Every controller in the repo shares these three entry points, so a device
// that is not open has to be refused here — by name. Before the guard the ioctl
// went out on whatever d.fd still held, which is descriptor 0 on a device that
// was never opened, and the caller got an errno it could not tell apart from a
// real device error.
func TestTransfersOnADeviceThatIsNotOpenAreRefusedByName(t *testing.T) {
	d := &HidrawDevice{Path: "/nonexistent/hidraw-test"}

	if err := d.SendFeatureReport(make([]byte, 9)); err == nil {
		t.Error("SendFeatureReport on a closed device returned no error")
	} else if !strings.Contains(err.Error(), "is not open") {
		t.Errorf("SendFeatureReport: %v", err)
	}

	if _, err := d.GetFeatureReport(0x00, 9); err == nil {
		t.Error("GetFeatureReport on a closed device returned no error")
	} else if !strings.Contains(err.Error(), "is not open") {
		t.Errorf("GetFeatureReport: %v", err)
	}

	if err := d.Write(make([]byte, 9)); err == nil {
		t.Error("Write on a closed device returned no error")
	} else if !strings.Contains(err.Error(), "is not open") {
		t.Errorf("Write: %v", err)
	}
}

// The same has to hold after a Close, which is the state the pulse daemon
// leaves the device in while it waits for the bar to come back.
func TestTransfersAfterCloseAreRefused(t *testing.T) {
	d := &HidrawDevice{Path: "/nonexistent/hidraw-test"}
	if err := d.Close(); err != nil {
		t.Fatalf("closing a device that was never open: %v", err)
	}
	if err := d.SendFeatureReport(make([]byte, 9)); err == nil ||
		!strings.Contains(err.Error(), "is not open") {
		t.Errorf("SendFeatureReport after Close: %v", err)
	}
}
