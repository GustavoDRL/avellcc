// Package fan provides fan monitoring and control for Clevo/Avell laptops.
package fan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Backend represents the fan control backend.
type Backend string

const (
	BackendTuxedoIO Backend = "tuxedo_io"
	BackendECRAM    Backend = "ec_ram"
	BackendACPICall Backend = "acpi_call"
	BackendHwmon    Backend = "hwmon"
	BackendNone     Backend = "none"
)

// TempReading holds a temperature sensor reading.
type TempReading struct {
	Name  string
	Label string
	Value float64
}

// FanStatus holds fan RPM and duty readings.
type FanStatus map[string]int

// FanController reads and controls fan speeds.
type FanController struct {
	backend Backend

	// acpiMethod is the ACPI path of the fan method, discovered at construction
	// rather than hard-coded, because it differs between firmwares.
	acpiMethod string
	// acpiVerified records whether that path actually answered a probe. Probing
	// needs root, so an unverified path is not the same as a missing one.
	acpiVerified bool

	// ecPath and ecRegs drive the embedded-controller backend. ecRegs is only
	// ever filled from a map measured on this exact model.
	ecPath string
	ecRegs ecRegisters
}

// NewFanController creates a new controller with auto-detected backend.
func NewFanController() *FanController {
	fc := &FanController{}

	if _, err := os.Stat("/dev/tuxedo_io"); err == nil {
		fc.backend = BackendTuxedoIO
		return fc
	}

	if _, err := os.Stat("/proc/acpi/call"); err == nil {
		path, verified := findACPIFanMethod()
		if path != "" {
			fc.backend, fc.acpiMethod, fc.acpiVerified = BackendACPICall, path, verified
			return fc
		}
	}

	matches, _ := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_input")
	if len(matches) > 0 {
		fc.backend = BackendHwmon
		return fc
	}

	// The EC comes last: it reports readings but no control, so anything above
	// is a better answer when it is present.
	if regs, ok := ecRegisterMaps[boardName()]; ok {
		if path := ecIOPath(); path != "" {
			fc.backend, fc.ecPath, fc.ecRegs = BackendECRAM, path, regs
			return fc
		}
	}

	fc.backend = BackendNone
	return fc
}

// Backend returns the detected backend name.
func (fc *FanController) Backend() Backend {
	return fc.backend
}

// GetFanRPM returns current fan RPMs and duty cycles.
func (fc *FanController) GetFanRPM() FanStatus {
	switch fc.backend {
	case BackendACPICall:
		return fc.acpiGetFans()
	case BackendHwmon:
		return fc.hwmonGetFans()
	case BackendTuxedoIO:
		return fc.hwmonGetFans()
	case BackendECRAM:
		return fc.ecFans()
	}
	return FanStatus{}
}

// GetTemperatures returns CPU and GPU temperature readings.
func (fc *FanController) GetTemperatures() []TempReading {
	skip := map[string]bool{
		"AC": true, "BAT0": true, "hidpp_battery_2": true,
		"ucsi_source_psy_USBC000:001": true, "ucsi_source_psy_USBC000:002": true,
		"acpi_fan": true,
	}

	var temps []TempReading
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	sort.Strings(hwmons)

	for _, hwmon := range hwmons {
		nameData, err := os.ReadFile(filepath.Join(hwmon, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(nameData))
		if skip[name] {
			continue
		}

		inputs, _ := filepath.Glob(filepath.Join(hwmon, "temp*_input"))
		sort.Strings(inputs)

		for _, tempInput := range inputs {
			valData, err := os.ReadFile(tempInput)
			if err != nil {
				continue
			}
			val, err := strconv.Atoi(strings.TrimSpace(string(valData)))
			if err != nil {
				continue
			}

			labelPath := strings.Replace(tempInput, "_input", "_label", 1)
			var label string
			if labelData, err := os.ReadFile(labelPath); err == nil {
				label = strings.TrimSpace(string(labelData))
			} else {
				base := filepath.Base(tempInput)
				idx := strings.TrimSuffix(strings.TrimPrefix(base, "temp"), "_input")
				label = "temp" + idx
			}

			var key string
			if name == "coretemp" {
				key = label
			} else {
				hwmonIdx := strings.TrimPrefix(filepath.Base(hwmon), "hwmon")
				key = fmt.Sprintf("%s[%s]: %s", name, hwmonIdx, label)
			}

			temps = append(temps, TempReading{
				Name:  key,
				Label: label,
				Value: float64(val) / 1000.0,
			})
		}
	}
	return temps
}

// SetFanSpeed sets fan speed as percentage (0-100). fanID: 0=both, 1=fan1, 2=fan2.
func (fc *FanController) SetFanSpeed(fanID, dutyPercent int) error {
	duty := dutyPercent * 255 / 100
	if duty < 0 {
		duty = 0
	}
	if duty > 255 {
		duty = 255
	}
	switch fc.backend {
	case BackendACPICall:
		return fc.acpiSetFan(fanID, duty)
	default:
		if hint := fc.BackendHint(); hint != "" {
			return fmt.Errorf("backend '%s' does not support fan control.\n%s", fc.backend, hint)
		}
		return fmt.Errorf("backend '%s' does not support fan control", fc.backend)
	}
}

// SetAuto returns fans to automatic control.
func (fc *FanController) SetAuto() error {
	switch fc.backend {
	case BackendACPICall:
		_, err := fc.acpiCall(0x69, 0x0F)
		return err
	default:
		return fmt.Errorf("backend '%s' does not support fan control", fc.backend)
	}
}

// --- acpi_call backend ---

// acpiFanMethods lists the ACPI paths where the Clevo-style fan method has been
// found. Upstream hard-coded the first one. That is not portable: the method
// lives under whatever name the vendor gave its PNP0C14 WMI device, and
// acpi_call reports a missing method by writing "Error: AE_NOT_FOUND" into its
// own output instead of failing the write — so a wrong path reads back as a
// successful call that returned zero.
var acpiFanMethods = []string{
	"\\_SB.WMI.WMBB",
	"\\_SB.PCI0.LPCB.EC0.WMBB",
	"\\_SB.PC00.LPCB.EC0.WMBB",
}

// ErrACPIRejected reports that the method exists but refused these arguments.
// It matters because the WMI sample code AMI ships in reference firmware
// answers every unrecognised selector with 0xFFFFFFFF. Masked to a byte that
// reads as a plausible 100% duty cycle, so a method carrying the right name and
// the right GUID can still be pure boilerplate with nothing behind it.
var ErrACPIRejected = errors.New("ACPI method rejected the request")

// ErrNoACPIMethod reports that no fan method was located.
var ErrNoACPIMethod = errors.New("no ACPI fan method found")

const acpiCallDev = "/proc/acpi/call"

func acpiCallAt(path string, method, arg int) (int, error) {
	cmd := fmt.Sprintf("%s 0x00 %#x %#x", path, method, arg)
	if err := os.WriteFile(acpiCallDev, []byte(cmd), 0); err != nil {
		return 0, fmt.Errorf("acpi_call write: %w", err)
	}
	data, err := os.ReadFile(acpiCallDev)
	if err != nil {
		return 0, fmt.Errorf("acpi_call read: %w", err)
	}
	return parseACPIReply(path, string(data))
}

// parseACPIReply turns one /proc/acpi/call reply into a value. acpi_call
// reports failures inside its output rather than through the read, so every
// non-numeric reply here used to be read as a successful call returning zero.
func parseACPIReply(path, raw string) (int, error) {
	result := strings.TrimRight(strings.TrimSpace(raw), "\x00")
	if rest, found := strings.CutPrefix(result, "Error:"); found {
		return 0, fmt.Errorf("%s: %s", path, strings.TrimSpace(rest))
	}
	hex, found := strings.CutPrefix(result, "0x")
	if !found {
		return 0, fmt.Errorf("%s: unexpected reply %q", path, result)
	}
	val, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("acpi_call parse '%s': %w", result, err)
	}
	if val == 0xFFFFFFFF {
		return 0, fmt.Errorf("%s: %w", path, ErrACPIRejected)
	}
	return int(val), nil
}

// findACPIFanMethod picks the first candidate path that answers a fan-duty read,
// and reports whether it was actually verified. Probing writes to
// /proc/acpi/call, which needs root; when the probe cannot run at all we fall
// back to the upstream path unverified, so that an unprivileged --status says
// "unverified" rather than claiming the machine has no backend.
// AVELLCC_ACPI_FAN_METHOD overrides the search.
func findACPIFanMethod() (string, bool) {
	if p := os.Getenv("AVELLCC_ACPI_FAN_METHOD"); p != "" {
		return p, false
	}
	blocked := false
	for _, p := range acpiFanMethods {
		_, err := acpiCallAt(p, 0x63, 0)
		if err == nil {
			return p, true
		}
		if errors.Is(err, os.ErrPermission) {
			blocked = true
			break
		}
	}
	if blocked {
		return acpiFanMethods[0], false
	}
	return "", false
}

func (fc *FanController) acpiCall(method, arg int) (int, error) {
	if fc.acpiMethod == "" {
		return 0, ErrNoACPIMethod
	}
	return acpiCallAt(fc.acpiMethod, method, arg)
}

func (fc *FanController) acpiGetFans() FanStatus {
	fans := FanStatus{}
	for fanNum, cmd := range map[int]int{1: 0x63, 2: 0x64} {
		raw, err := fc.acpiCall(cmd, 0)
		if err != nil {
			continue
		}
		if raw != 0 {
			duty := raw & 0xFF
			fans[fmt.Sprintf("fan%d_duty", fanNum)] = duty
			fans[fmt.Sprintf("fan%d_duty_pct", fanNum)] = duty * 100 / 255
		}
	}
	// Supplement with hwmon RPM
	hwmonFans := fc.hwmonGetFans()
	for k, v := range hwmonFans {
		fans[k] = v
	}
	return fans
}

func (fc *FanController) acpiSetFan(fanID, duty int) error {
	var arg int
	if fanID == 0 {
		arg = (duty & 0xFF) | ((duty & 0xFF) << 8)
	} else {
		raw1, _ := fc.acpiCall(0x63, 0)
		raw2, _ := fc.acpiCall(0x64, 0)
		duty1 := raw1 & 0xFF
		duty2 := raw2 & 0xFF
		if duty1 == 0 {
			duty1 = duty
		}
		if duty2 == 0 {
			duty2 = duty
		}
		switch fanID {
		case 1:
			duty1 = duty
		case 2:
			duty2 = duty
		}
		arg = (duty1 & 0xFF) | ((duty2 & 0xFF) << 8)
	}
	_, err := fc.acpiCall(0x68, arg)
	return err
}

// --- hwmon backend (read-only) ---

func (fc *FanController) hwmonGetFans() FanStatus {
	fans := FanStatus{}
	inputs, _ := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_input")
	sort.Strings(inputs)
	fanIdx := 0
	for _, fanInput := range inputs {
		data, err := os.ReadFile(fanInput)
		if err != nil {
			continue
		}
		rpm, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		fanIdx++
		fans[fmt.Sprintf("fan%d_rpm", fanIdx)] = rpm
	}
	return fans
}

// StatusReport generates a human-readable fan and temperature status report.
func StatusReport(fc *FanController) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Backend: %s", fc.backend))

	fans := fc.GetFanRPM()
	if len(fans) > 0 {
		lines = append(lines, "\nFans:")
		// Collect fan numbers
		fanNums := map[string]bool{}
		for k := range fans {
			if strings.HasPrefix(k, "fan") {
				parts := strings.SplitN(strings.TrimPrefix(k, "fan"), "_", 2)
				fanNums[parts[0]] = true
			}
		}
		sorted := make([]string, 0, len(fanNums))
		for n := range fanNums {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, fn := range sorted {
			rpm, hasRPM := fans["fan"+fn+"_rpm"]
			dutyPct, hasDuty := fans["fan"+fn+"_duty_pct"]
			var parts []string
			if hasRPM {
				parts = append(parts, fmt.Sprintf("Fan %s: %d RPM", fn, rpm))
			} else {
				parts = append(parts, fmt.Sprintf("Fan %s: ? RPM", fn))
			}
			if hasDuty {
				parts = append(parts, fmt.Sprintf("(duty: %d%%)", dutyPct))
			}
			lines = append(lines, "  "+strings.Join(parts, "  "))
		}
		if level, ok := fc.ECFanLevel(); ok {
			lines = append(lines, fmt.Sprintf("  EC curve step: %d", level))
		}
	} else {
		lines = append(lines, "\nNo fan data available.")
		if hint := fc.BackendHint(); hint != "" {
			lines = append(lines, hint)
		}
	}

	temps := fc.GetTemperatures()
	if len(temps) > 0 {
		lines = append(lines, "\nTemperatures:")
		var coreTemps []float64
		for _, t := range temps {
			if strings.HasPrefix(t.Name, "Core ") {
				coreTemps = append(coreTemps, t.Value)
			} else {
				lines = append(lines, fmt.Sprintf("  %s: %.1f°C", t.Name, t.Value))
			}
		}
		if len(coreTemps) > 0 {
			min, max := coreTemps[0], coreTemps[0]
			for _, v := range coreTemps[1:] {
				if v < min {
					min = v
				}
				if v > max {
					max = v
				}
			}
			lines = append(lines, fmt.Sprintf("  CPU Cores (%d): %.0f-%.0f°C", len(coreTemps), min, max))
		}
	}

	return strings.Join(lines, "\n")
}

// ACPIFan describes one ACPI fan object (PNP0C0B) as the kernel exposes it
// through the thermal cooling-device class.
type ACPIFan struct {
	Name     string
	MaxState int
}

// ACPIFanObjects returns the firmware's ACPI fan objects. A MaxState of 1 means
// the firmware offers on/off switching only: no tachometer, no duty cycle. Such
// fans exist on machines that report no fan RPM at all, which is worth saying
// out loud rather than leaving the absence unexplained.
func ACPIFanObjects() []ACPIFan {
	var fans []ACPIFan
	dirs, _ := filepath.Glob("/sys/class/thermal/cooling_device*")
	sort.Strings(dirs)
	for _, dir := range dirs {
		typeData, err := os.ReadFile(filepath.Join(dir, "type"))
		if err != nil || strings.TrimSpace(string(typeData)) != "Fan" {
			continue
		}
		maxState := -1
		if d, err := os.ReadFile(filepath.Join(dir, "max_state")); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(d))); err == nil {
				maxState = v
			}
		}
		fans = append(fans, ACPIFan{Name: filepath.Base(dir), MaxState: maxState})
	}
	return fans
}

// HasUniwillEC reports whether the firmware exposes a Uniwill embedded-controller
// node, which is what would make the tuxedo_io driver relevant.
//
// It deliberately skips UNIW* nodes that declare a compatible ID for something
// else. Uniwill's registered ACPI vendor prefix appears on any device its
// firmware engineers named, not only on the EC: on the Avell Storm 470 the sole
// UNIW* node is UNIW0001, an I2C touchpad with _CID PNP0C50. Treating that as
// proof of a Uniwill fan interface sends users to install a DKMS driver on the
// strength of a pointing device.
func HasUniwillEC() bool {
	return hasUniwillECIn("/sys/bus/acpi/devices")
}

func hasUniwillECIn(root string) bool {
	dirs, _ := filepath.Glob(filepath.Join(root, "UNIW*"))
	for _, dir := range dirs {
		cid, _ := os.ReadFile(filepath.Join(dir, "cid"))
		if strings.Contains(string(cid), "PNP0C50") {
			continue // HID-over-I2C peripheral, says nothing about the EC
		}
		return true
	}
	return false
}

// HasUniwillPlatformDevice reports whether the firmware exposes the ACPI device
// that the in-tree uniwill_laptop driver binds to. This is the node that
// actually matters, and it is not the one carrying Uniwill's vendor prefix:
// the driver binds to INOU0000, while UNIW* on this machine is a touchpad.
//
// The driver does not autoload here. Its module aliases are DMI-based and cover
// TUXEDO and Schenker machines, so a rebadged unit never matches even though the
// driver works once loaded.
func HasUniwillPlatformDevice() bool {
	matches, _ := filepath.Glob("/sys/bus/acpi/devices/INOU*")
	return len(matches) > 0
}

// uniwillWMIGUIDs is the GUID block tuxedo-drivers' uniwill_wmi probes for. Its
// probe checks nothing else, so any firmware carrying these GUIDs satisfies it.
var uniwillWMIGUIDs = []string{
	"ABBC0F6D-8EA1-11D1-00A0-C90629100000",
	"ABBC0F6E-8EA1-11D1-00A0-C90629100000",
	"ABBC0F6F-8EA1-11D1-00A0-C90629100000",
	"ABBC0F70-8EA1-11D1-00A0-C90629100000",
	"ABBC0F71-8EA1-11D1-00A0-C90629100000",
	"ABBC0F72-8EA1-11D1-00A0-C90629100000",
}

// hasUniwillWMIGUIDs reports whether the full block is advertised.
func hasUniwillWMIGUIDs() bool {
	for _, guid := range uniwillWMIGUIDs {
		matches, _ := filepath.Glob("/sys/bus/wmi/devices/" + guid + "*")
		if len(matches) == 0 {
			return false
		}
	}
	return true
}

// BackendHint explains what would be needed to get further with fan support. It
// reports what was actually probed, because every shortcut here — guessing the
// barebone vendor from an ACPI prefix, trusting a WMI GUID — has a false
// positive that costs someone an afternoon.
func (fc *FanController) BackendHint() string {
	switch fc.backend {
	case BackendNone:
		lines := []string{"No fan backend detected."}

		if HasUniwillPlatformDevice() {
			lines = append(lines, "",
				"This machine has the INOU0000 ACPI device, which the in-tree",
				"uniwill_laptop driver binds to. It reports fan RPM, PWM and CPU/GPU",
				"temperatures through hwmon. It does not autoload here — its module",
				"aliases match on TUXEDO and Schenker DMI strings only:",
				"  sudo modprobe uniwill_laptop",
				"  echo uniwill_laptop | sudo tee /etc/modules-load.d/uniwill.conf")
			return strings.Join(lines, "\n")
		}

		if _, known := ecRegisterMaps[boardName()]; known && ecIOPath() == "" {
			lines = append(lines, "",
				"This model's EC registers are known, but its register space is not",
				"readable here. It lives in debugfs, so this needs root and the ec_sys",
				"module:",
				"  sudo modprobe ec_sys && sudo avellcc fan --status")
		}

		if fans := ACPIFanObjects(); len(fans) > 0 {
			onOff := true
			for _, f := range fans {
				if f.MaxState > 1 {
					onOff = false
				}
			}
			if onOff {
				lines = append(lines, fmt.Sprintf(
					"The firmware exposes %d ACPI fan object(s), but each is an on/off cooling",
					len(fans)),
					"device with no tachometer, so none of them can report RPM.")
			}
		}

		if _, err := os.Stat("/proc/acpi/call"); err != nil {
			lines = append(lines, "",
				"Clevo barebones reach fan control through an ACPI method, via acpi_call:",
				"  sudo pacman -S --needed acpi_call && sudo modprobe acpi_call")
		} else {
			lines = append(lines, "",
				"acpi_call is loaded, but none of the known fan methods answered:",
				"  "+strings.Join(acpiFanMethods, "  "),
				"Set AVELLCC_ACPI_FAN_METHOD to point at another ACPI path.")
		}

		switch {
		case HasUniwillEC():
			lines = append(lines, "",
				"This machine exposes a Uniwill EC node. tuxedo-drivers provides",
				"/dev/tuxedo_io for those:",
				"  yay -S tuxedo-drivers-nocompatcheck-dkms && sudo modprobe tuxedo_io")
		case hasUniwillWMIGUIDs():
			lines = append(lines, "",
				"Do not install tuxedo-drivers on the strength of the WMI GUIDs alone.",
				"This machine advertises the whole ABBC0F6x block that uniwill_wmi probes",
				"for, but no Uniwill EC node backs it: on the Avell Storm 470 that block is",
				"the WMI sample code AMI ships in its reference firmware. The driver would",
				"bind on the false positive, and its EC helper reaches a method that feeds",
				"caller bytes straight into raw EC writes. See docs/storm470-fans.md.")
		}
		return strings.Join(lines, "\n")

	case BackendECRAM:
		return "Readings come straight from the embedded controller. Setting fan speed\n" +
			"would mean writing to it, which this fork does not do — the same controller\n" +
			"runs battery charging and thermal shutdown. See docs/storm470-fans.md."

	case BackendACPICall:
		if !fc.acpiVerified {
			return fmt.Sprintf("Using ACPI method %s, unverified — probing it needs root.", fc.acpiMethod)
		}
	case BackendHwmon:
		return "hwmon exposes readings only. Setting fan speed needs acpi_call (Clevo) or\n" +
			"tuxedo_io (Uniwill/TongFang)."
	}
	return ""
}
