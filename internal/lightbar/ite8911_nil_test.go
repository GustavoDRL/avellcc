package lightbar

import "testing"

// NewITE8911(nil) leaves c.dev nil until Open() has succeeded, and GetFeature
// and SendFeature went straight through it — the same shape as the nil
// dereference that took the pulse daemon down on the ITE 8233. Latent here only
// because every path in cmd/ opens first and returns on the error.
func TestITE8911WritesErrorWhenNothingWasOpened(t *testing.T) {
	c := NewITE8911(nil)

	if _, err := c.GetFeature(0xCD, 8); err == nil {
		t.Error("GetFeature on an unopened controller reported success")
	}
	if err := c.SendFeature(0xCD, []byte{1, 2, 3}, 8); err == nil {
		t.Error("SendFeature on an unopened controller reported success")
	}
	if err := c.SendCommand(0x01, []byte{1}, 8); err == nil {
		t.Error("SendCommand on an unopened controller reported success")
	}
	if err := c.X58Off(); err == nil {
		t.Error("X58Off on an unopened controller reported success")
	}
}
