package fan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acpi_call answers a missing or unhappy method inside its own output rather
// than by failing the write, so every one of these replies used to be read as
// a successful call. The first two returned the value zero — a fan reporting
// 0% duty — and the last returned 255, a fan reporting 100%.
func TestACPIReplyParsing(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		want    int
		wantErr error
		errHas  string
	}{
		{name: "duty", reply: "0x2b\n", want: 0x2b},
		{name: "missing method", reply: "Error: AE_NOT_FOUND\n\x00", errHas: "AE_NOT_FOUND"},
		{name: "non numeric", reply: "ABCDEFGH\n", errHas: "unexpected reply"},
		{name: "sample fallthrough", reply: "0xffffffff\n", wantErr: ErrACPIRejected},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseACPIReply("\\_SB.TEST.WMBB", tc.reply)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got err %v, want %v", err, tc.wantErr)
				}
			case tc.errHas != "":
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("got err %v, want one containing %q", err, tc.errHas)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %d, want %d", got, tc.want)
				}
			}
		})
	}
}

// The Storm 470's only UNIW* node is its I2C touchpad. Reading that as a
// Uniwill EC is what sent fan --status recommending an unsafe driver.
func TestUniwillECIgnoresHIDPeripherals(t *testing.T) {
	dir := t.TempDir()

	mk := func(name, cid string) {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if cid != "" {
			if err := os.WriteFile(filepath.Join(d, "cid"), []byte(cid+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	mk("UNIW0001:00", "PNP0C50") // the touchpad
	if hasUniwillECIn(dir) {
		t.Fatal("an I2C HID touchpad was accepted as a Uniwill EC")
	}

	mk("UNIW0002:00", "") // something that is not a HID peripheral
	if !hasUniwillECIn(dir) {
		t.Fatal("a non-HID UNIW node was rejected")
	}
}
