# ITE 8291 rev 3 over Linux hidraw

Protocol reference for the ITE 8291 per-key RGB keyboard controller, as driven
by `internal/keyboard/ite8291.go`. Verified on an Avell Storm 470
(`048d:600b`).

Applies to product IDs `048d:6004`, `048d:6006`, `048d:600b` and `048d:ce00`,
all at `bcdDevice` 0x0003.

## Finding the device

The USB device exposes two HID interfaces: a plain boot keyboard and a vendor
interface that carries the LED protocol. Only the second may be written to.
Match it by its report descriptor, which begins with usage page `0xFF03`:

```
06 03 ff    Usage Page (Vendor 0xFF03)
09 01       Usage
a1 01       Collection (Application)
15 00       Logical Minimum (0)
26 ff 00    Logical Maximum (255)
75 08       Report Size (8 bits)
95 40       Report Count (64)
09 20  81 02    Input   report, 64 bytes
09 21  91 02    Output  report, 64 bytes
09 22  95 08  b1 02    Feature report, 8 bytes
c0          End Collection
```

Matching on product ID alone is not enough, which is why `FindITE8291` reads
`/sys/class/hidraw/hidrawN/device/report_descriptor` and checks that prefix.

## The report-ID byte

All three reports are **unnumbered**. The kernel still treats `buf[0]` as a
report-ID slot and strips it before the bytes reach the wire, so every hidraw
buffer is one byte longer than the report it carries:

| Operation | Call | Buffer | On the wire |
|---|---|---|---|
| Command | `HIDIOCSFEATURE` | 9 bytes: `[0x00, cmd, control, …]` | 8 bytes |
| Reply | `HIDIOCGFEATURE` | 9 bytes; data lands at `buf[1]` | 8 bytes |
| Colours | `write()` | 65 bytes: `[0x00, payload…]` | 64 bytes |

> **This is where the widely-cited reference diverges.** `pobrn/ite8291r3-ctl`
> drives the device through libusb and writes 65 *raw* bytes to endpoint `0x02`,
> so its byte layout sits one position off from the hidraw framing above. Code
> ported from it without accounting for the stripped report-ID byte produces
> colours shifted by one channel. The framing documented here was confirmed on
> real hardware by filling the keyboard red, green, blue and white in turn.

## Commands

Feature report, 8 bytes: `[cmd, control, arg…]`.

| Command | Value | Payload |
|---|---|---|
| `SET_EFFECT` | `0x08` | `[0x08, control, effect, speed, brightness, colour, direction, save]` |
| `SET_BRIGHTNESS` | `0x09` | `[0x09, 0x02, level]` — level 0–50 |
| `SET_PALETTE` | `0x14` | `[0x14, 0x00, index, r, g, b]` — index 1–7 |
| `SET_ROW_INDEX` | `0x16` | `[0x16, 0x00, row]` |
| `GET_FW_VERSION` | `0x80` | send, then read: `[echo, hi, lo, test, customer, …]` |
| `GET_EFFECT` | `0x88` | send, then read: `[echo, control, effect, speed, brightness, colour, …]` |

`control` is `0x02` to apply and `0x01` to switch the backlight off.

Built-in animations, passed as `effect` to `SET_EFFECT`: breathing `0x02`,
wave `0x03`, random `0x04`, rainbow `0x05`, ripple `0x06`, marquee `0x09`,
raindrop `0x0A`, aurora `0x0E`, fireworks `0x11`.

## Per-key colour

Two behaviours differ from the Clevo ITE 8295 and are easy to miss:

1. **User mode.** Per-key colours only take effect after `SET_EFFECT` with
   effect id **51**, which hands control to the host. Starting a hardware
   animation or switching the backlight off cancels it, and the controller's LED
   buffer is then undefined — so re-entering user mode must be followed by a
   full repaint, not a single-row update.
2. **Brightness range.** The hardware scale is **0–50**. avellcc keeps its 0–10
   scale across all controllers, so the driver rescales.

Colours go out one grid row at a time: set the row index, then write the row.
The 64-byte payload is **all blues, then all greens, then all reds**, followed
by one padding byte:

```
byte  0..20   blue,  columns 0-20
byte 21..41   green, columns 0-20
byte 42..62   red,   columns 0-20
byte 63       padding
```

With the leading report-ID byte, the hidraw buffer is 65 bytes.

## Reading state back

The controller reports colours nowhere — `GET_EFFECT` returns the current
animation, brightness and colour index, but never the LED buffer. Two
consequences shape the driver:

- **The framebuffer must be mirrored.** `internal/keyboard/ite8291.go` keeps a
  6×21×3 mirror in `~/.config/avellcc/ite8291-framebuffer.bin`. Without it every
  new process would start from an all-black framebuffer, and setting one key
  would blank the rest of its row. Writes are throttled so software effects do
  not hammer the disk.
- **Startup state is read, not assumed.** `Open` issues `GET_EFFECT` so a fresh
  process inherits the real brightness and knows whether user mode is already
  active, instead of repainting needlessly.

`GET_EFFECT` is also why there is no way to ask the hardware which key sits
under which LED — see [`storm470-keymap.md`](storm470-keymap.md).
