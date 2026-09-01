# Avell Storm 470 support

Upstream avellcc targets the **Avell Storm 590X**, a Clevo barebone. The
**Storm 470** carries different silicon, so this fork adds a second keyboard
driver alongside the original and wires the result into the Omarchy theme
system.

The Storm 470 *is* Uniwill-based, but not for the reason earlier revisions of
these notes gave: they cited the `UNIW0001` ACPI node, which is the touchpad.
The node that matters is `INOU0000`, and the in-tree `uniwill_laptop` driver
binds to it. Both the wrong turn and the working setup are in
[`storm470-fans.md`](storm470-fans.md).

| | Storm 590X (upstream) | Storm 470 (this fork) |
|---|---|---|
| Keyboard RGB | `048d:8910` — ITE 8295, 6×20 grid | `048d:600b` — **ITE 8291 rev 3**, 6×21 grid |
| Lightbar | `048d:8911` | present — `048d:7001` ITE 8233, driven through the EC |
| Fans | Clevo WMI `ABBC0F6D` | `uniwill_laptop` on `INOU0000` — readings only, no control |

Fan and temperature readings need the `uniwill_laptop` module, which does not
autoload here; `avellcc fan --status` then reports both fans through hwmon.

## Documentation

| Document | Contents |
|---|---|
| [`ite8291-protocol.md`](ite8291-protocol.md) | The wire protocol: device discovery, hidraw framing, commands, per-key colour, why the framebuffer is mirrored |
| [`storm470-keymap.md`](storm470-keymap.md) | The LED grid map, how to derive one on another machine, and what three failed attempts taught |
| [`omarchy-integration.md`](omarchy-integration.md) | Theme hook, resume handling, udev |
| [`storm470-fans.md`](storm470-fans.md) | Getting fan readings, why there is no fan control, and why the obvious driver is unsafe here |
| [`storm470-ec-inou.md`](storm470-ec-inou.md) | The `INOU0000` EC accessor itself, and how it differs from the dead `AMW0` WMI block |
| [`lightbar-re.md`](lightbar-re.md) | The chassis light bar: two candidate controllers, and the write that latches it off |
| [`fork-changes.md`](fork-changes.md) | What changed against upstream, bugs fixed, what is still open |

## The short version

Three things about this hardware account for most of the work:

1. **The hidraw framing differs from the usual reference.** The reports are
   unnumbered, so the kernel strips a leading byte that the libusb-based
   `ite8291r3-ctl` keeps. Code ported without noticing produces shifted colours.
2. **The controller cannot be read back.** It never reports its LED buffer, so
   the driver mirrors a framebuffer to disk; and it cannot say which key sits
   under which LED, so the key map has to be measured by hand.
3. **The LED grid is scrambled.** Grid rows do not follow the keyboard's
   physical rows, and each row starts at a different column. Nothing about the
   layout predicts this.

## Install on this machine

```bash
# udev — GROUP="input" also covers system services, which get no uaccess ACL,
# and --action=add matters: a plain trigger sets the mode but fires no ACL
sudo install -m 644 udev/60-avellcc-storm470.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules
sudo udevadm trigger --action=add --subsystem-match=hidraw

go build -o avellcc . && install -Dm755 avellcc ~/.local/bin/avellcc

omarchy hook install theme-set omarchy/50-avellcc-keyboard
install -Dm755 omarchy/avellcc-resume-monitor ~/.local/bin/avellcc-resume-monitor
install -Dm644 omarchy/avellcc-keyboard.service ~/.config/systemd/user/avellcc-keyboard.service
systemctl --user daemon-reload && systemctl --user enable --now avellcc-keyboard.service
```

Go was not present on this machine; it came from `mise` (`mise use -g go@latest`).

## Checking it works

```bash
avellcc keyboard firmware     # controller, grid size, firmware version
avellcc keyboard layout       # the calibrated grid
avellcc keyboard --color red  # whole keyboard
avellcc keyboard --key esc --color blue
omarchy theme set nord        # the keyboard should follow
```
