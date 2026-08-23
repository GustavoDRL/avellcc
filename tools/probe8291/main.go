//go:build ignore || probe

// Command probe8291 validates the ITE 8291 rev3 protocol against real hardware
// before the driver is wired into avellcc. It is a throwaway diagnostic tool.
package main

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	numRows = 6
	numCols = 21

	// Feature report: 8 bytes, no report ID -> 9-byte hidraw buffer.
	ctrlLen = 9
	// Output report: 64 bytes, no report ID -> 65-byte hidraw buffer.
	rowLen = 65

	cmdSetEffect     = 0x08
	cmdSetBrightness = 0x09
	cmdSetRowIndex   = 0x16
	cmdGetFWVersion  = 0x80
	cmdGetEffect     = 0x88

	userModeEffect = 51
	maxBrightness  = 50
)

func hidiocsfeature(n int) uintptr { return 3<<30 | uintptr('H')<<8 | 0x06 | uintptr(n)<<16 }
func hidiocgfeature(n int) uintptr { return 3<<30 | uintptr('H')<<8 | 0x07 | uintptr(n)<<16 }

type dev struct{ fd int }

func (d *dev) sendCtrl(payload ...byte) error {
	buf := make([]byte, ctrlLen) // buf[0] = report ID 0 (unnumbered reports)
	copy(buf[1:], payload)
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), hidiocsfeature(ctrlLen), uintptr(unsafe.Pointer(&buf[0])))
	if e != 0 {
		return fmt.Errorf("HIDIOCSFEATURE: %w", e)
	}
	return nil
}

func (d *dev) getCtrl() ([]byte, error) {
	buf := make([]byte, ctrlLen)
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), hidiocgfeature(ctrlLen), uintptr(unsafe.Pointer(&buf[0])))
	if e != 0 {
		return nil, fmt.Errorf("HIDIOCGFEATURE: %w", e)
	}
	return buf[1:], nil // kernel leaves buf[0] as the report-ID slot
}

// writeRow pushes one row of colours. framing selects the byte layout under test:
// "A" = [rid, B*21, G*21, R*21, pad]  (report ID stripped by the kernel)
// "B" = [rid, pad, B*21, G*21, R*20]  (mimics the pyusb 65-byte raw endpoint write)
func (d *dev) writeRow(row int, rgb [numCols][3]byte, framing string) error {
	if err := d.sendCtrl(cmdSetRowIndex, 0x00, byte(row)); err != nil {
		return err
	}
	buf := make([]byte, rowLen)
	base := 1
	if framing == "B" {
		base = 2
	}
	for i := 0; i < numCols; i++ {
		if base+2*numCols+i < rowLen {
			buf[base+0*numCols+i] = rgb[i][2] // blue
			buf[base+1*numCols+i] = rgb[i][1] // green
			buf[base+2*numCols+i] = rgb[i][0] // red
		}
	}
	n, err := unix.Write(d.fd, buf)
	if err != nil {
		return fmt.Errorf("write row %d: %w", row, err)
	}
	if n != rowLen {
		return fmt.Errorf("write row %d: short write %d/%d", row, n, rowLen)
	}
	return nil
}

func (d *dev) enableUserMode(brightness byte) error {
	// [SET_EFFECT, control=0x02, effect, speed, brightness, colour, direction, save]
	return d.sendCtrl(cmdSetEffect, 0x02, userModeEffect, 0x00, brightness, 0x00, 0x00, 0x00)
}

func (d *dev) fill(r, g, b byte, framing string) error {
	var row [numCols][3]byte
	for i := range row {
		row[i] = [3]byte{r, g, b}
	}
	for y := 0; y < numRows; y++ {
		if err := d.writeRow(y, row, framing); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	path := "/dev/hidraw1"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	framing := "A"
	if len(os.Args) > 2 {
		framing = os.Args[2]
	}

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
		os.Exit(1)
	}
	d := &dev{fd: fd}
	defer func() { _ = unix.Close(fd) }()

	fmt.Printf("device: %s   framing: %s\n\n", path, framing)

	if err := d.sendCtrl(cmdGetFWVersion); err != nil {
		fmt.Fprintf(os.Stderr, "get fw: %v\n", err)
		os.Exit(1)
	}
	if buf, err := d.getCtrl(); err == nil {
		fmt.Printf("firmware raw : % 02x\n", buf)
		fmt.Printf("firmware ver : %d.%d.%d.%d  (high.low.test.customer)\n", buf[1], buf[2], buf[3], buf[4])
	}

	_ = d.sendCtrl(cmdGetEffect)
	if buf, err := d.getCtrl(); err == nil {
		fmt.Printf("effect state : % 02x\n", buf)
	}

	fmt.Printf("\nenabling user mode (brightness %d)...\n", maxBrightness)
	if err := d.enableUserMode(maxBrightness); err != nil {
		fmt.Fprintf(os.Stderr, "user mode: %v\n", err)
		os.Exit(1)
	}
	if err := d.sendCtrl(cmdSetBrightness, 0x02, maxBrightness); err != nil {
		fmt.Fprintf(os.Stderr, "brightness: %v\n", err)
	}

	steps := []struct {
		name    string
		r, g, b byte
	}{
		{"RED", 255, 0, 0},
		{"GREEN", 0, 255, 0},
		{"BLUE", 0, 0, 255},
		{"WHITE", 255, 255, 255},
	}
	for _, s := range steps {
		fmt.Printf("  -> filling %s ... watch the keyboard\n", s.name)
		if err := d.fill(s.r, s.g, s.b, framing); err != nil {
			fmt.Fprintf(os.Stderr, "fill %s: %v\n", s.name, err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
	}

	fmt.Println("\ncolumn ramp: col 0 red -> col 20 blue (checks column alignment)")
	var ramp [numCols][3]byte
	for i := 0; i < numCols; i++ {
		f := byte(i * 255 / (numCols - 1))
		ramp[i] = [3]byte{255 - f, 0, f}
	}
	for y := 0; y < numRows; y++ {
		if err := d.writeRow(y, ramp, framing); err != nil {
			fmt.Fprintf(os.Stderr, "ramp: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("done — leaving the ramp on screen for inspection.")
}
