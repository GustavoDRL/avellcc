# What this fork changes

Local fork of `hugo-andrade/avellcc`, adapted for the Avell Storm 470. No
upstream contribution intended.

## Second keyboard driver

Upstream drives one controller through the concrete type `*keyboard.ITE8295`.
This fork introduces `keyboard.Controller`, an interface both drivers implement,
and `keyboard.NewController()` which detects what is present. The ITE 8291 is
probed first, because it identifies itself by a vendor HID collection — a
stronger signal than a bare product ID match.

| File | Role |
|---|---|
| `internal/keyboard/controller.go` | interface, detection, effect-name union |
| `internal/keyboard/ite8291.go` | the new driver |
| `internal/keyboard/ite8295.go` | upstream's driver, unchanged in behaviour |

The interface carries `Rows()`/`Cols()`, `KeymapID()` and `DefaultKeymap()`
because the two controllers differ in grid size and wiring, and `HWEffects()`
because their built-in animations are different sets.

## Bugs fixed

**Software effects died silently.** `reload` and `--profile` both started an
effect in a goroutine and returned immediately; the process exited and the
animation stopped without a word. `--profile` now keeps the process alive.
`reload` is a one-shot that cannot host an animation, so it says so instead of
pretending to have restored one.

**`--speed` never reached the hardware.** The ITE 8291 driver sent a hard-coded
constant. Speed is now passed through; the ITE 8295's animation command has no
speed field and documents that it ignores the value.

**Brightness was assumed, not read.** Each new process started from a default
rather than the controller's actual level. `Open` now issues `GET_EFFECT`.

**One key map for two keyboards.** Upstream's `DefaultMap` describes the ITE
8295's 6×20 grid. Reusing it for the 8291's 6×21 grid lit the wrong keys and
looked like a hardware fault. Maps are now per controller —
see [`storm470-keymap.md`](storm470-keymap.md).

**A touchpad was read as a fan backend.** `IsUniwill()` globbed
`/sys/bus/acpi/devices/UNIW*` and concluded the machine was a Uniwill barebone.
The only `UNIW*` node on the Storm 470 is its I2C touchpad. On that basis
`fan --status` told users to install a DKMS driver that is unsafe on this
hardware. The check now skips nodes whose `_CID` marks them as HID-over-I2C.

**A missing ACPI method read as a fan at 0%.** `acpiCall` treated any reply
that did not start with `0x` as the value zero, and `acpi_call` reports a
missing method by writing `Error: AE_NOT_FOUND` into its own output rather than
by failing the write. Errors now surface, and `0xFFFFFFFF` — the fall-through
of every WMI sample method — is rejected instead of being masked to a
plausible-looking 100% duty.

**The ACPI method path was hard-coded.** `\_SB.WMI.WMBB` does not exist on
every machine; it does not exist on this one. The path is now discovered from a
candidate list, overridable with `AVELLCC_ACPI_FAN_METHOD`.

**Calibration destroyed data.** The rewritten `calibrate` used to delete any
earlier position holding the same key name, so sweeping a numeric keypad erased
the number row. Collisions now resolve into `num_*` names.

## Loose ends closed

- The interactive panel hard-coded "ITE 8295" in its header and listed that
  controller's effects. Both now reflect the detected controller.
- The layout preview was dead code. It is now `avellcc keyboard layout`, which
  renders the calibrated grid — plain text when not on a terminal.
- `fan --status` reported a bare `Backend: none`. It now reports what was
  actually determined: how many ACPI fan objects exist and why they carry no
  RPM, whether an ACPI method answered, and — when the machine advertises the
  Uniwill WMI GUIDs without a Uniwill EC behind them — that installing
  tuxedo-drivers on that signal is a mistake.
- The lightbar error reported a lookup failure. It now says the machine has no
  rear lightbar — which is true of the Clevo ITE 8911 bar, but the machine does
  have a chassis bar on a second MCU. See below.

## Additions

- `internal/fan/ecram.go` — reads fan tachometers, duty and CPU temperature
  straight out of the embedded controller, for machines whose fans are invisible
  to ACPI and hwmon. Gated on the DMI board name: the register map is per model
  and applying one to the wrong controller reports nonsense as fact.
- `avellcc keyboard layout` — render the calibrated grid.
- `avellcc keyboard calibrate --step N` — anchor sweep.
- `tools/gridpaint` — light grid positions directly, no key map involved.
- `tools/probe8291` — standalone protocol prober over raw hidraw.
- `internal/lightbar/ite8233.go` — a second lightbar driver. Upstream drives the
  Clevo ITE 8911 rear bar; this machine has a chassis bar on an ITE 8233
  (`048d:7001`), which takes RGB triples instead of a fixed palette of colour
  IDs. `avellcc lightbar` now detects which controller is present and routes:
  `cmd/lightbar_ite8233.go` handles the RGB one, and the X58 path is untouched.
  The two keep separate saved state. Protocol and the hazard that comes with it:
  [`storm470-lightbar.md`](storm470-lightbar.md).

Both tools are excluded from normal builds by a `probe` build tag:

```bash
go build -tags probe -o /tmp/gridpaint ./tools/gridpaint/
go build -tags probe -o /tmp/probe8291 ./tools/probe8291/
```

## Not addressed

**Fan speed control.** Fan *readings* work: load the in-tree `uniwill_laptop`
driver and `avellcc fan --status` reports both fans through hwmon with no
special support. Setting speed is not available, and not because of anything
this fork lacks — the driver exposes `pwm` read-only on purpose, because the
registers behind it are unstable on some models. See
[`storm470-fans.md`](storm470-fans.md), which also covers why installing
`tuxedo-drivers` here would be actively unsafe.

**The `/` key** has no LED on this machine and therefore cannot be addressed.
