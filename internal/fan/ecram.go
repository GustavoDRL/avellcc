package fan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Reading fans straight out of the embedded controller.
//
// Some machines keep fan tachometers and duty in EC registers and expose them
// through no ACPI method, no WMI interface and no hwmon device. The Avell Storm
// 470 is one: see docs/storm470-fans.md for the firmware evidence. The registers
// below were found by dumping the EC once a second across idle, load and
// cool-down and keeping the bytes that moved.
//
// The map is per model and this package refuses to apply one to a machine it
// was not measured on. Register numbers mean entirely different things on
// different embedded controllers, and reporting another vendor's battery
// threshold as a fan speed is worse than reporting nothing.

// ecRegisters records where one model's EC keeps its readings. Tach entries are
// {high, low} byte pairs, one per fan.
type ecRegisters struct {
	CPUTemp  int
	FanLevel int
	FanDuty  int
	Tach     [][2]int
}

// ecRegisterMaps is keyed by DMI board name.
//
// Storm 470, measured 2026-08-22. 0x3E is the anchor that proves the rest: it
// tracked coretemp sample for sample, and the DSDT declares CPTM at offset
// 0x43E of the EC's memory window — which fixes the window's 0x4XX offsets to
// standard EC registers 0xXX. The same equivalence puts the DSDT's FFAN, a
// four-bit field declared and never referenced, exactly at 0x60, where the
// observed values 4..10 fit.
var ecRegisterMaps = map[string]ecRegisters{
	"STORM 470": {
		CPUTemp:  0x3E,
		FanLevel: 0x60,
		FanDuty:  0x61,
		Tach:     [][2]int{{0x64, 0x65}, {0x6C, 0x6D}},
	},
}

// ErrECUnreadable reports that the EC exists but could not be read.
var ErrECUnreadable = errors.New("EC register space is not readable")

func boardName() string {
	data, err := os.ReadFile("/sys/class/dmi/id/board_name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ecIOPath returns the debugfs handle onto the EC's register space, which the
// ec_sys module provides. Reading it needs root; the module defaults to
// read-only and nothing here asks for more.
func ecIOPath() string {
	matches, _ := filepath.Glob("/sys/kernel/debug/ec/*/io")
	sort.Strings(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// readECSpace reads the EC's 256-byte register space in one pass, so that every
// register in a report comes from the same instant.
func readECSpace(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 256)
	n, err := f.ReadAt(buf, 0)
	if err != nil && n < len(buf) {
		return nil, err
	}
	return buf, nil
}

// ecFans reads fan RPM and duty using the map for this machine.
func (fc *FanController) ecFans() FanStatus {
	fans := FanStatus{}
	if fc.ecPath == "" {
		return fans
	}
	space, err := readECSpace(fc.ecPath)
	if err != nil {
		return fans
	}

	for i, pair := range fc.ecRegs.Tach {
		rpm := int(space[pair[0]])<<8 | int(space[pair[1]])
		fans[fmt.Sprintf("fan%d_rpm", i+1)] = rpm
	}
	if fc.ecRegs.FanDuty != 0 {
		duty := int(space[fc.ecRegs.FanDuty])
		for i := range fc.ecRegs.Tach {
			fans[fmt.Sprintf("fan%d_duty", i+1)] = duty
			fans[fmt.Sprintf("fan%d_duty_pct", i+1)] = duty * 100 / 255
		}
	}
	return fans
}

// ECFanLevel returns the EC's own fan-curve step, which is not a percentage of
// anything — it indexes a table inside the controller.
func (fc *FanController) ECFanLevel() (int, bool) {
	if fc.ecPath == "" || fc.ecRegs.FanLevel == 0 {
		return 0, false
	}
	space, err := readECSpace(fc.ecPath)
	if err != nil {
		return 0, false
	}
	return int(space[fc.ecRegs.FanLevel]), true
}
