# avellcc

<p>
    <a href="https://goreportcard.com/report/github.com/hugo-andrade/avellcc"><img src="https://goreportcard.com/badge/github.com/hugo-andrade/avellcc" alt="Go Report Badge"></a>
    <a href="https://github.com/hugo-andrade/avellcc/actions/workflows/ci.yml"><img src="https://github.com/hugo-andrade/avellcc/actions/workflows/ci.yml/badge.svg" alt="CI Badge"></a>
    <a href="https://github.com/hugo-andrade/avellcc/blob/main/LICENSE"><img src="https://img.shields.io/github/license/hugo-andrade/avellcc.svg" alt="License Badge"></a>
    <a href="https://github.com/hugo-andrade/avellcc/releases"><img src="https://img.shields.io/github/v/release/hugo-andrade/avellcc" alt="Release Badge"></a>
</p>

Linux control center for **Avell Storm 590X** (Clevo barebone) laptops. Per-key RGB keyboard LEDs, rear lightbar, fan control, and thermal monitoring — no Windows required.

Single static binary, zero dependencies.

> ### This fork, and what it adds
>
> Everything below the **Hardware** table is upstream's. This fork also supports
> the **Avell Storm 470**, which is a different machine in three places, and the
> difference matters before you type anything:
>
> * **Keyboard:** ITE 8291 rev 3 (`048d:600b` on this machine), not the IT8295 —
>   different report layout and a 6x21 grid. See
>   [`docs/storm470-ite8291.md`](docs/storm470-ite8291.md).
> * **Light bar:** the Storm 470 has no rear X58 bar. It has an **ITE 8233
>   chassis light bar** (`048d:7001`) that speaks another protocol, with its own
>   effects, a 0-100 brightness range and arbitrary RGB. See
>   [`docs/storm470-lightbar.md`](docs/storm470-lightbar.md).
> * **Fans:** readings need the `uniwill_laptop` module and fan **speed control
>   is not available** — see [`docs/storm470-fans.md`](docs/storm470-fans.md).
>
> On top of the hardware, this fork carries the Omarchy integration: the theme
> paints the keyboard (`avellcc keyboard --theme`) and the bar
> (`avellcc lightbar --theme`), a `~/.config/avellcc/lightbar.toml` holds the
> settings for both, and a pulse daemon can drive the bar from the music. All of
> it is in [`docs/omarchy-integration.md`](docs/omarchy-integration.md), and the
> full list of divergences from upstream is in
> [`docs/fork-changes.md`](docs/fork-changes.md).
>
> **None of this is in an upstream release.** On a Storm 470, build this fork
> from source (below) — the `curl | bash` quick install fetches an upstream
> release binary, which has neither the ITE 8291 keyboard nor the ITE 8233 bar
> nor `lightbar config`.

## Hardware

| Component | Storm 590X (upstream) | Storm 470 (this fork) |
|---|---|---|
| Keyboard LED Controller | ITE IT8295, USB `048d:8910`, 6x20 grid | ITE 8291 rev 3, USB `048d:600b` (also `6004`, `6006`, `ce00`), 6x21 grid |
| Light bar Controller | ITE `048d:8911`, rear bar (X58 protocol) | ITE 8233, USB `048d:7001`, chassis bar |
| Fans | 2x ACPI fans via WMI/hwmon | read-only via `uniwill_laptop`; no speed control |
| WMI | Clevo WMBB (`ABBC0F6D`) for fan control | — |

The controller is detected at runtime: the light bar command looks for an ITE
8233 first and falls back to the X58 rear bar. Which one you have decides which
effects, colours and brightness range apply — see [Light bar](#light-bar).

## Install

### Quick install (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/hugo-andrade/avellcc/main/install.sh | bash
```

This downloads the latest release, verifies the checksum, installs the binary to `/usr/local/bin`, sets up udev rules, and installs the systemd restore service.

You can customize the install:

```bash
# Install a specific version
VERSION=0.2.0 curl -fsSL https://raw.githubusercontent.com/hugo-andrade/avellcc/main/install.sh | bash

# Install to a custom directory
INSTALL_DIR=~/.local/bin curl -fsSL https://raw.githubusercontent.com/hugo-andrade/avellcc/main/install.sh | bash
```

### Go install

```bash
go install github.com/hugo-andrade/avellcc@latest
```

> **Note:** `go install` only installs the binary. You still need to set up udev rules manually for non-root access (see [udev rules](#udev-rules) below).

### Build from source

```bash
git clone https://github.com/hugo-andrade/avellcc.git
cd avellcc
make install
```

Or manually:

```bash
go build -o avellcc .
sudo install -m 755 avellcc /usr/local/bin/
```

### udev rules

Required for non-root access to the keyboard and lightbar HID devices:

```bash
sudo cp udev/99-avellcc.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

### Fan speed control (optional)

Fan speed control requires the `acpi_call` kernel module:

```bash
# Arch Linux
sudo pacman -S --needed linux-headers acpi_call-dkms

# Debian / Ubuntu
sudo apt install dkms acpi-call-dkms linux-headers-$(uname -r)
```

```bash
sudo modprobe acpi_call
```

## Usage

### Keyboard

```bash
avellcc keyboard --color red              # All keys solid color
avellcc keyboard --color "#FF6600"        # Hex color
avellcc keyboard --color 255,100,0        # RGB values

avellcc keyboard --key w --color blue     # Single key
avellcc keyboard --key space --color green

avellcc keyboard --brightness 7           # Brightness (0-10)
avellcc keyboard --effect rainbow         # Hardware animation
avellcc keyboard --effect sw_rainbow      # Software rainbow wave
avellcc keyboard --effect sw_breathing    # Software breathing

avellcc keyboard --profile gaming.json    # Load profile
avellcc keyboard --theme                  # Colour and brightness from the Omarchy theme
avellcc keyboard --off                    # Turn off

avellcc kb -c red -b 7                    # Short alias
```

### Light bar

**The two vocabularies below barely overlap**, so a Storm 590X example run on a
Storm 470 gets you either an error or a bar that looks dead. The answer that is
always right for the machine in front of you comes from the binary itself: on an
ITE 8233, `avellcc lightbar` with no flags prints the state plus that
controller's effects, colour syntax, brightness and speed ranges; on an ITE
8911 it prints the same kind of list when stdout is not a terminal, and opens
the interactive panel when it is.

#### Storm 590X — rear bar, ITE 8911

```bash
avellcc lightbar                          # Show status and available effects/colors
avellcc lightbar --effect static --color blue --brightness 4
avellcc lightbar --effect wave --speed 5
avellcc lightbar --effect color-wave
avellcc lightbar --effect change-color
avellcc lightbar --effect granular --color cyan
avellcc lightbar --off

avellcc lb -e static -c blue -b 4 -s 3   # Short alias
```

Effects: `static`, `breathe`, `wave`, `change-color`, `granular`, `color-wave`

Colors: `red`, `yellow`, `lime`, `green`, `cyan`, `blue`, `purple`. Brightness is
`0-4`.

#### Storm 470 — chassis bar, ITE 8233

```bash
avellcc lightbar                          # Status, effects and ranges for this bar
avellcc lightbar --effect static --color '#FFB6D1' --brightness 80
avellcc lightbar --effect breathing --speed 4
avellcc lightbar --effect bounce
avellcc lightbar --theme                  # Colour from the current Omarchy theme
avellcc lightbar --off
```

Effects: `static`, `breathing`, `wave`, `bounce`, `marquee`, `scan` — note
`breathing`, not `breathe`. Colour is **arbitrary RGB**, not a palette id:
`#RRGGBB`, bare `RRGGBB`, or one of the seven ITE 8911 names, which are mapped
to their hex so the same word works on both controllers. **Brightness is
`0-100`**, so a `--brightness 4` copied from the block above is 4% and looks
like a bar that is off.

(`avellcc keyboard --color` is a different parser and does take `R,G,B` and
eleven names; the light bar does not.)

Settings for the theme colour, the pulse daemon and the keyboard backlight live
in one file:

```bash
avellcc lightbar config show              # What is in force, and where it came from
avellcc lightbar config show --json
avellcc lightbar config keys              # Every settable key
avellcc lightbar config path
avellcc lightbar config set pulse.gain 2.0
```

The file has three sections — `[theme]`, `[pulse]` and `[keyboard]` — and the
last one is what `avellcc keyboard --theme` reads for brightness and for which
`colors.toml` key to take. See
[`docs/omarchy-integration.md`](docs/omarchy-integration.md).

### Fans

```bash
avellcc fan                               # Live TUI dashboard (interactive terminal)
avellcc fan --status                      # Plain text output
avellcc fan --speed 80                    # All fans 80%
avellcc fan --speed 100 --fan 1           # Fan 1 at 100%
avellcc fan --auto                        # Back to automatic
```

The TUI dashboard shows live RPM sparklines, duty progress bars, and temperatures. Keyboard shortcuts: `+` max, `-` min, `a` auto, `q` quit.

### Keyboard utilities

```bash
avellcc keyboard keys                     # List known key names
avellcc keyboard keys -v                  # With grid positions
avellcc keyboard calibrate                # Interactive key-to-LED calibration
avellcc keyboard firmware                 # Show keyboard firmware info
```

## Profiles

JSON files in `~/.config/avellcc/profiles/`:

```json
{
    "brightness": 10,
    "color": "black",
    "lightbar": {
        "effect": "static",
        "color": "blue",
        "brightness": 4,
        "speed": 3
    },
    "keys": {
        "w": "#FF0000",
        "a": "#FF0000",
        "s": "#FF0000",
        "d": "#FF0000",
        "space": "#FF4400",
        "esc": "#FFFFFF"
    }
}
```

## State reload

```bash
avellcc reload                            # Reload saved keyboard and lightbar state
```

### Restore on boot

```bash
sudo cp systemd/avellcc.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable avellcc.service
```

### Restore on suspend/resume

```bash
sudo install -Dm755 systemd/system-sleep/avellcc /usr/lib/systemd/system-sleep/avellcc
```

Both the boot service and suspend/resume hook call `avellcc reload` to restore saved keyboard and lightbar state.

> **Tip:** The quick install script sets up both automatically.

## Uninstall

```bash
make uninstall
```

Or manually:

```bash
sudo systemctl disable --now avellcc.service
sudo rm -f /usr/local/bin/avellcc
sudo rm -f /etc/udev/rules.d/99-avellcc.rules
sudo rm -f /etc/systemd/system/avellcc.service
sudo rm -f /usr/lib/systemd/system-sleep/avellcc
sudo udevadm control --reload-rules
sudo systemctl daemon-reload
```

## Protocol

### Keyboard (ITE IT8295)

HID feature reports on report ID `0xCC` (6 bytes) via Linux hidraw.

| Command | Format | Description |
|---|---|---|
| Set key color | `CC 01 <led_id> R G B` | Per-key RGB |
| Set brightness | `CC 09 <level> 02 00 00` | Level 0-10 |
| Hardware animation | `CC 00 09 00 00 00` | Random color effect |

LED addressing: `led_id = (row << 5) | col` on a 6x20 grid.

### Light bar (ITE 8911, rear)

HID feature reports on report ID `0xCD`, command `0xE2`, 64-byte frames via hidraw. Protocol reverse-engineered from the Windows `CC.Device.LightBar_X58` driver. Details in [`docs/lightbar-re.md`](docs/lightbar-re.md).

### Light bar (ITE 8233, chassis)

A different MCU on the same bus, reached by its vendor usage page (`0xFF03`)
rather than by interface number. The byte that has to match the exact product is
the variant that follows each command — `0x22` for the `048d:7001` on the Storm
470. Details in [`docs/storm470-lightbar.md`](docs/storm470-lightbar.md).

### Fans (Clevo WMI)

ACPI method `\_SB.WMI.WMBB` (GUID `ABBC0F6D`, 3 args: instance, command, data).

| Command | Function |
|---|---|
| `0x63` | Get fan 1 duty + period |
| `0x64` | Get fan 2 duty + period |
| `0x68` | Set fan duty (packed: fan1[7:0] \| fan2[15:8]) |
| `0x69` | Set auto mode (bitmask: bit0=fan1, bit1=fan2) |

## Compatibility

Built and tested on Arch Linux. Should work on any distro with hidraw support. Other Clevo-based laptops with ITE IT8295 (TUXEDO, Sager, etc.) should also work.

The Storm 470 support in this fork was written against one machine, and every
protocol claim in `docs/storm470-*.md` says whether it was measured there or
taken from tuxedo-drivers. Uniwill/TongFang barebones with the same ITE 8233
(`048d:6010`, `048d:7000`) are listed in the code but were never tried.
