# Fan control on the Storm 470

Short answer: fan **readings** work through the in-tree `uniwill_laptop` driver,
which does not autoload here. Fan **control** is not available, and the driver
that looks like it would provide it is unsafe to install on this machine.

```bash
sudo modprobe uniwill_laptop
echo uniwill_laptop | sudo tee /etc/modules-load.d/uniwill.conf
```

That gives `fan1_input`, `fan2_input`, `pwm1`, `pwm2` and CPU/GPU temperatures
through hwmon, which `avellcc fan --status` picks up with no special support.

The node itself is documented separately, in
[`storm470-ec-inou.md`](storm470-ec-inou.md) — what its accessor does, and why
it is real EC RAM where the `AMW0` WMI block is not. This document is about the
fans.

## The device that is easy to miss

The driver binds to **`INOU0000`** — `\_SB.INOU` in the DSDT. Searching for
Uniwill's `UNIW*` vendor prefix does not find it, and on this machine that
prefix belongs to the touchpad. Searching for WMI GUIDs finds only the dead
sample block described below. `INOU0000` is the node that matters.

It does not autoload: the module's aliases are DMI-based and cover TUXEDO and
Schenker machines, so a rebadged unit never matches even though the driver works
perfectly once loaded. That is what `/etc/modules-load.d` above is for.

## Why there is no fan control

`pwm1` is `0444`, and there is no `pwm1_enable`. This is deliberate. From the
driver's review on LKML:

> Those two registers technically allow for manual fan control, but are unstable
> on some models and are likely not meant to be used by applications as they are
> only accessible when using the WMI interface.

The hwmon ops declare `.visible = 0444` and implement no write handler. The
person who reverse-engineered this controller looked at manual control and
deliberately backed away from it.

## What the firmware does not have

The rest of this document is the firmware survey that preceded finding
`INOU0000`. Its conclusions about ACPI and WMI still hold — they are why the
`INOU0000` driver is the only route — but the survey missed that node, so read
it as "no fan interface *through ACPI methods or WMI*", not as "no interface".

That survey is drawn from the firmware itself — the DSDT and all fifteen
SSDTs, decompiled with `iasl` — not from probing, so it can be re-checked
without touching hardware:

```bash
sudo cp /sys/firmware/acpi/tables/{DSDT,SSDT*} .   # tables are 0400 root
iasl -d DSDT                                       # acpica package
```

**No fan method.** Upstream avellcc calls `\_SB.WMI.WMBB` through `acpi_call`.
No device named `WMI` exists in this firmware. The only `PNP0C14` WMI device is
`\_SB.AMW0`, and no SSDT adds another.

**No tachometer.** Five ACPI fan objects exist — `\_TZ_.FAN0` through `FAN4`,
surfaced as `cooling_device0..4` — but they are legacy `PNP0C0B` fans backed by
power resources, `max_state = 1`. On or off, no RPM, no duty cycle. None of the
ACPI 4.0 fan objects (`_FIF`, `_FPS`, `_FSL`, `_FST`) is present.

**No usable Intel path.** `\_SB.PTID` advertises "CPU Fan Duty Cycle" and
"CPU Fan #1 Speed" in RPM, which looks promising until you read `RPMD`: every
field it returns is read through `\_SB.PC00.LPCB.H_EC`, and that EC device's
`_STA` returns zero. It is Intel reference code wired to an embedded controller
this board does not enable. The live EC is `\_SB.PC00.LPCB.EC0`, and the one
fan-shaped field in it — `FFAN`, four bits at offset `0x460` of the EC's memory
window — is declared and never referenced by anything.

So the fans are managed entirely inside the EC, by firmware, through a register
map the ACPI tables never describe.

## Why the WMI GUIDs are a trap

`avellcc fan --status` used to advise installing `tuxedo-drivers`, and this
machine appears to confirm that advice twice over. Both signals are false.

**The `UNIW0001` node is a touchpad.** It carries `_CID PNP0C50` and binds as an
I2C HID device — `Device (TPAD)` in the DSDT. Uniwill's registered ACPI vendor
prefix appears on whatever device its firmware engineers named, and a pointing
device says nothing about the EC. The fork's old `IsUniwill()` globbed
`/sys/bus/acpi/devices/UNIW*` and read this as proof of a Uniwill barebone.

**The Clevo/Uniwill WMI GUIDs are boilerplate.** `\_SB.AMW0` advertises
`ABBC0F6A` through `ABBC0F72` — including `ABBC0F6D`, which tuxedo-drivers uses
as both `CLEVO_WMI_METHOD_GUID` and `UNIWILL_WMI_MGMT_GUID_BA`. Following them
into the ASL shows what is behind them:

```asl
Name (SAB0, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!@#$%^&*()...")
Method (GETB, 1) { If ((Arg0 == Zero)) { Return (SAB0) } ... }
Method (WMBB, 3) { If ((Arg1 == One)) { Return (GETB (Arg0)) } ... }
```

Alphabet strings and counting buffers: the WMI-ACPI sample implementation AMI
ships in its reference firmware. Clevo and Uniwill both derived their real
interfaces from the same Microsoft sample, which is why the GUIDs collide.

## Why installing tuxedo-drivers would be worse than useless

`uniwill_wmi`'s probe checks the six GUIDs and nothing else:

```c
status = wmi_has_guid(UNIWILL_WMI_EVENT_GUID_0) && ... && wmi_has_guid(UNIWILL_WMI_MGMT_GUID_BC);
if (!status) return -ENODEV;
```

All six are present here, so the driver binds and logs `interface initialized`.
Its EC accessor then calls method id `4` on `ABBC0F6F` — `WMBC` — passing a
buffer whose bytes 4–7 it fills with the requested function number. Follow that
branch in the ASL:

```asl
If ((Arg1 == 0x04)) { AC00 = Arg2; OEMG (AC00); SAC1 = Zero; ... Return (AC00) }
```

`AC00` is overwritten with the caller's buffer *before* `OEMG` runs, so `OEMG`
dispatches on bytes the driver chose. Its selectors are not stubs:

| `SAC1` | Reached from function | What it does |
|---|---|---|
| `0x0000` | 0 | `WKBC` — writes `LDAT`/`HDAT`/`CMDL`/`CMDH` into `EC0` and raises `WFLG` |
| `0x0100` | 1 | `RKBC` — raw EC read |
| `0x0200` | 2 | `SCMD` — sends an arbitrary EC command |
| `0x0300` | 3 | `EC0.IGPS()` / `EC0.DGPS()` — discrete GPU power switching |

So the driver would not merely read nonsense. Its EC helper lands on live
embedded-controller writes, EC commands, and GPU power switching, with argument
bytes laid out for a different machine's EC. The method returns a buffer and
sets no ACPI error, so the driver reads back its own input as if it were EC
data and reports success throughout.

The `nocompatcheck` variant is the dangerous one specifically because
`tuxedo_compatibility_check` — the DMI gate that would reject a non-TUXEDO
machine — is what it removes.

There is a second, smaller conflict: `ite_8291` in the same package claims
`048d:600b`, the keyboard this fork drives. Installing it puts a second driver
on the controller.

## What the code does about it now

- `HasUniwillEC()` replaces `IsUniwill()` and skips `UNIW*` nodes whose `_CID`
  marks them as HID-over-I2C peripherals. It returns false here, correctly.
- The ACPI method path is discovered from a candidate list instead of
  hard-coded, overridable with `AVELLCC_ACPI_FAN_METHOD`.
- `acpiCallAt` surfaces `Error: AE_NOT_FOUND` as an error. It previously
  returned zero for it, so a missing method read as a fan at 0% duty. It also
  rejects `0xFFFFFFFF`, the fall-through every sample WMI method returns —
  masked to a byte that would otherwise read as a plausible 100% duty.
- `BackendHint()` warns when the GUID block is present without a Uniwill EC
  behind it.

## The EC register map, measured

Before `INOU0000` turned up, the register map was derived by hand: dump the EC's
256-byte space once a second across idle, load and cool-down, and keep the bytes
that move. Nine of 256 moved.

| Register | Meaning | idle → load |
|---|---|---|
| `0x3E` | CPU temperature, °C | 70 → 95 |
| `0x60` | fan curve step (4 bits) | 4 → 10 |
| `0x61` | fan duty | 100 → 180 |
| `0x64`/`0x65` | fan 1 tachometer, 16-bit big-endian | 3433 → 4568 |
| `0x6C`/`0x6D` | fan 2 tachometer, 16-bit big-endian | 3477 → 4607 |

`0x3E` is what makes the rest trustworthy: it tracked `coretemp` sample for
sample, and the DSDT declares `CPTM` at offset `0x43E` of the EC's memory
window — which fixes that window's `0x4XX` offsets to standard EC registers
`0xXX`. The same equivalence lands `FFAN`, a four-bit field declared and never
referenced, exactly on `0x60`, where the observed values 4–10 fit.

The map was later confirmed against the driver: at the same instant, the
hand-decoded `0x64`/`0x65` read 4558 and the driver's `fan1_input` read 4539.

`internal/fan/ecram.go` implements this as a fallback backend for when
`uniwill_laptop` is unavailable. It is gated on the DMI board name, because
register numbers mean different things on different controllers and reporting
another vendor's battery threshold as a fan speed is worse than reporting
nothing. Reading needs root — the EC's register space lives in debugfs, via
`ec_sys`.

Note these are **not** the addresses tuxedo-drivers uses. It reads duty at
`0x1804`/`0x1809`; here the values sit in the standard 256-byte space. Another
sign that this is not the controller layout tuxedo knows.

## What would be needed for control

Writing `0x60`/`0x61`, which is exactly what the upstream driver author declined
to expose. Not attempted here either, for the same reason plus one more: the
same controller runs battery charging and thermal shutdown.
