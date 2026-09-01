# Omarchy theme integration

The keyboard backlight and the chassis light bar follow the desktop theme:
change the theme and both colours change with it, restored at login and after
resume.

## How the colour is chosen

`omarchy theme set` ends by running `omarchy-hook theme-set <slug>`, so the
integration lives in a user hook and never touches `/usr/share/omarchy/`, which
is overwritten on update.

```
omarchy theme set <name>
  └─ omarchy-hook theme-set <slug>
       └─ ~/.config/omarchy/hooks/theme-set.d/50-avellcc-keyboard
            ├─ reads accent from ~/.local/state/omarchy/current/theme/colors.toml
            └─ avellcc keyboard --color <hex> --brightness 8
```

Omarchy ships `omarchy-theme-set-keyboard`, but it dispatches only to ASUS ROG
and Framework 16 keyboards, and it reads a `keyboard.rgb` file that **exactly one
stock theme provides** — `tokyo-night`, as `ff00ff`, a magenta that does not
track that theme's palette. Honouring it would mean a magenta keyboard on one
theme and no effect at all on the other twenty-one.

The hook therefore takes `accent`, which every theme defines and which is the
colour that most reads as the theme's identity.

The whole hook is now one line, `avellcc keyboard --theme`, and what it does is
configured in the `[keyboard]` section of `~/.config/avellcc/lightbar.toml` —
the same file the light bar and the pulse daemon read.

| Key in `[keyboard]` | Default | Meaning |
|---|---|---|
| `color_key` | `accent` | which `colors.toml` key to take |
| `brightness` | `8` | backlight level, 0–10 |
| `enabled` | `true` | paint the keyboard on a theme switch at all |

Any key in `colors.toml` works, so `color_key = "bright_magenta"` or
`"foreground"` are valid choices. Custom and installed themes work the same way
— they carry a `colors.toml` like the stock ones.

`color_key = "accent"` is not just a default, it is the point: `accent` is the
entry the now-playing integration overrides with the colour of the current
wallpaper, and `--theme` resolves colours through `CurrentColors()`, which
applies that override. So the wallpaper decides, and the hook's first write is
already the right colour.

Three environment variables used to live here — `AVELLCC_THEME_COLOR_KEY`,
`AVELLCC_THEME_BRIGHTNESS` and `AVELLCC_THEME_USE_KEYBOARD_RGB`. The first two
became `color_key` and `brightness` above. The third has no replacement: it was
opt-in, off by default, and `keyboard.rgb` does not track its theme's palette.
`avellcc keyboard --color <hex>` still sets a colour by hand.

Every exit path in the hook is a success. A theme switch must never fail because
the keyboard is missing or busy.

## The light bar takes a contrasting hue from the same palette

`51-avellcc-lightbar` is a second, independent hook — the two controllers are
different hardware, either can be absent, and neither should be able to break
the other's theme switch.

It cannot simply read a second fixed key. A theme whose `accent` is already blue
would put `bright_blue` on the bar and blue on the keyboard, two near-identical
colours side by side, and `orange` and `brown` do not exist in three of the
stock themes at all. So the bar takes whichever of the theme's own palette
colours — `red`, `yellow`, `green`, `cyan`, `blue`, `magenta` and their
`bright_` variants — sits farthest around the hue circle from the accent.
Contrast is guaranteed on every theme, and the colour still belongs to the
theme. Saturation only breaks ties.

That rule used to be an awk script inside the hook. It now lives in
`internal/omarchy/palette.go` behind `avellcc lightbar --theme`, and the hook
is a thin wrapper around it. The move was forced by the pulse daemon below,
which needs the same choice in memory and re-made on every theme switch: two
implementations of "which colour is the theme's" would have drifted apart
without anything failing.

On parity with the awk it replaced, this document used to claim more than it
could support. The `omarchy/` directory has never been committed, so no version
of the awk hook exists to compare against, and the test's expected values come
from this table rather than from a run of the awk. There is exactly one real
data point: on `catppuccin`, `--theme` produced `#f9e2af`, which is what the awk
hook had already written into `state.json`. Treat the rest as the rule
reimplemented and tested, not as parity proven. `internal/omarchy/palette_test.go` asserts the Go
picks against the real stock theme files. Note that the assertions and this
table are not the same set: the test pins `tokyo-night`, `everforest`,
`gruvbox`, `retro-82` and `catppuccin`, while the last row below is
`bring-me-the-horizon`, which is not a stock theme and is not asserted. A
separate test sweeps *every* installed theme for the one property that must
hold everywhere — that no two bands collapse onto the same colour.

| Theme | keyboard (`accent`) | light bar |
|---|---|---|
| tokyo-night | `#7aa2f7` blue | `#e0af68` yellow |
| everforest | `#7fbbb3` teal | `#e67e80` red |
| gruvbox | `#7daea3` teal | `#d3869b` magenta |
| retro-82 | `#faa968` orange | `#028391` teal |
| bring-me-the-horizon | `#ffb6d1` pink | `#6e8f7a` green |

`vantablack` and `white` are fully monochrome: no hue to contrast with and no
saturated colour to fall back on. There the bar matches the accent, because the
theme itself has one colour.

The port also tightened two floors the awk version did not have: candidates now
need saturation ≥ 0.20 **and** value ≥ 0.35, where before only `s ≥ 0.15` was
required. Saturation alone admits colours that are technically hued and
practically invisible.

An earlier version of this paragraph justified the change with two examples,
and an audit showed both were wrong. `solitude` was said to have picked a
near-black `#343d41`; it did not — that was its *treble*, and the hook writes
*mid*, which was and remains a strong `#de6145`. And `lumon` did pick a
near-white `#d1eef8` before, but under the new floors it picks `#b4e4f6` —
another near-white. The floors did not fix `lumon`.

What they did fix is narrower and worth stating exactly: they keep a near-black
out of the *treble* slot, which matters now that three bands are derived rather
than one. None of the picks in the table above change.

Every knob lives in `~/.config/avellcc/lightbar.toml`, described under
**One file for both halves** below. The picker can be exercised without a theme
switch:

```bash
avellcc lightbar --theme                    # what the hook would write
avellcc lightbar --theme --theme-key red    # a fixed key, overriding the file
avellcc lightbar --show-config              # what is in force, and where from
```

## The bar pulses with the music, in the theme's colours

`avellcc lightbar --pulse` is a foreground daemon that paints the bar in time
with what is playing. It owns the bar **only while the player is playing**; on
pause, on stop and on exit it restores the saved state.

Note what "the saved state" means, because this used to be described as "the
theme stays the bar's resting appearance" and that is not true in the interval:
every write saves state, so a colour set by hand — `--color`, or a click on one
of the bar widget's band swatches — becomes the resting colour until the next
`omarchy theme set` overwrites it. The theme is the resting appearance *after a
theme switch*; between switches, the last deliberate write is.

```
avellcc lightbar --pulse
  ├─ busctl        → resolves pulse.player to the bus name it owns now
  ├─ dbus-monitor  → MPRIS PlaybackStatus of that name
  ├─ cava          → 9 spectrum bars, raw binary, at --pulse-fps
  └─ /dev/hidrawN  → 2 packets per frame, on a handle opened once
```

The `N` is deliberate: the number is enumeration order, not an address. It was
`hidraw2` when this was written and is `hidraw1` today, with the touchpad now
holding `hidraw2` — nothing here hardcodes it, and neither should you. The
device is found by its vendor usage page; `avellcc lightbar config show --json`
prints the path in force.

`pulse.player` names the player, not the exact bus name. Some players never
own the bare name: Omarchy Spotify's backend publishes
`org.mpris.MediaPlayer2.OmarchySpotify.instance<pid>`, because mpris-server
requires each instance to be unique. The daemon asks the bus for the live
name inside that namespace, and the `NameOwnerChanged` rule watches the whole
namespace, so a restarted player is picked up on its new instance.

### Why cava, and why it paces the loop

The spectrum comes from cava rather than an FFT written here. cava already
solves PipeWire capture, windowing, per-bar frequency distribution and
perceptual smoothing, and its raw output mode is a stable interface. Reading it
also paces the frame loop for free: cava emits at its configured rate, so there
is no timer in the daemon to drift against it. The config is generated into
`~/.cache/avellcc/cava-pulse.conf` on every start rather than shipped, because
the bar count and frame rate have to agree with the reader on the other end of
the pipe — an edited copy that disagreed would desynchronise the frames rather
than fail loudly.

### The band that is loudest is not the band that carries the rhythm

Bass energy dominates nearly every frame of nearly every track. Choosing the
colour by absolute band energy would pin the bar to the accent forever and the
feature would look broken in a way that is hard to diagnose.

So each band is scored against **its own** running average instead, and the
band currently holding the colour keeps it until another beats it by a margin
(`Dominance`, 1.15). What moves the colour is a band rising above its own
recent behaviour — which is what a listener hears as the rhythm — and the
margin is what stops two near-equal bands trading the colour every frame.

| Band | Colour |
|---|---|
| bass — bars 1–3 | the theme's `accent` |
| mid — bars 4–6 | the contrast colour the theme-set hook uses |
| treble — bars 7–9 | the colour farthest in hue from *both* of the above |

Brightness follows overall loudness with an asymmetric filter — attack 0.85,
decay 0.12 — so a beat arrives instantly and fades. A symmetric filter reads as
flicker rather than as rhythm. The floor is 12, not 0: a bar that goes fully
dark between beats reads as broken.

The gain above the mean is 2.0, and that number is measured rather than chosen.
`tools/pulsereplay` replays a recording of real cava output through the mapper;
counting only the frames that actually carry music, the ceiling reached is 64 at
gain 1.25, 96 at 2.0, and 100 with the top 5% clipped flat at 2.5.

### The mapping is tested against recorded audio, not tone generators

`internal/pulse/testdata/frames.bin` is 50 seconds of real cava output from this
machine's capture, in exactly the format the daemon reads. It exists because an
audit's two most severe claims about this algorithm — that the least dynamic
band holds the colour ~93% of the time, and that the colour flips 4-10 times a
second into a mush — were derived from synthetic signals and **do not
reproduce** on real input. Measured on the recording:

| | synthetic claim | recorded audio |
|---|---|---|
| accent (bass) share | "effectively never" | 39% |
| colour changes | 4-10 per second | 1.8 per second |
| colour reaches a palette colour | "never" | 82% of frames |

Steady tones plus a periodic kick are not what cava emits; autosens and its
smoothing make the real thing noisier and slower. The finding was still worth
having — measuring it is what surfaced the gain being half of what it should be,
which no audit spotted — but the algorithm was not changed on synthetic evidence.

One honest limit: removing the dominance margin entirely does not push the flip
rate past the test's bound on this recording, so the margin is not demonstrably
load-bearing on real audio.

Colour changes ease rather than jump (`ColorEase`, 0.30 per frame). Switching
hue the instant the dominant band changes strobes badly at 30 fps.

`internal/pulse` holds all of this and knows nothing about audio sources or
HID, so every rule above is covered by tests that need neither hardware nor
sound.

### Write rate

The theme hooks write two packets per theme change. Pulsing writes two per
frame, continuously, and this controller answers an unwelcome packet with
success — so the rate was measured rather than assumed, with `tools/pulserate`,
sweeping hue so a stall is visible rather than inferred:

```console
$ go run ./tools/pulserate --rates 5,10,20,30,60 --seconds 3
device /dev/hidraw2 (048d:7001)   # the number has changed since; see above

target   achieved   frames     write err    worst write
5        5.0        15         0            1.432814ms
10       10.0       30         0            1.346013ms
20       19.7       60         0            1.883126ms
30       29.3       88         0            1.5471ms
60       58.1       175        0            1.770013ms
```

No write errors at any rate, and a frame's two packets cost under 2 ms — 11% of
the budget at 60 fps. The default is 30 fps, which leaves the MCU idle 95% of
the time.

Every one of these is a key in `~/.config/avellcc/lightbar.toml` and a flag on
`avellcc lightbar`, with the same name; see below.

### Following the player without a D-Bus library

The gate is MPRIS `PlaybackStatus`, read by parsing one long-lived
`dbus-monitor` subprocess — the same choice `avellcc-resume-monitor` already
makes for logind's `PrepareForSleep`. A match rule naming a well-known sender
is resolved by the bus, so it works whether or not Spotify is running yet, and
`NameOwnerChanged` triggers a re-query rather than a guess when the player
appears or disappears.

The parsing is a pure function over the monitor's text, so a recorded session
drives it in a test — including the case that looks like a bug: a track whose
title happens to be the string `PlaybackStatus`.

## One file for both halves

`~/.config/avellcc/lightbar.toml` is the only place to configure any of this.
The daemon writes it, commented, the first time it starts, and never rewrites
it — so edits and comments survive every upgrade.

```toml
[theme]                  # what the bar shows at rest
enabled = true
brightness = 80          # 0-100
effect = "static"        # static, breathing, wave, bounce, marquee, scan
speed = 5                # 1 (fastest) to 10; animated effects only
color_key = "auto"       # or any key from the theme's colors.toml

[pulse]                  # what it does while music plays
enabled = true
fps = 30
min_brightness = 12
max_brightness = 100
gain = 2.0
player = "spotify"       # short name, or a full MPRIS bus name
input_method = "pipewire"
input_source = "auto"
```

There used to be two places: five `AVELLCC_THEME_LIGHTBAR_*` environment
variables for the hook, and flags baked into the systemd unit for the daemon.
Two syntaxes for one feature, neither discoverable from the other, and the
daemon could not read the hook's half at all. The environment variables are
gone.

**Keys mostly mirror flag names.** `pulse.fps` is `--pulse-fps`, and a flag
wins over the file, so a value can be tried before it is committed.

The mirror is not a substitute for reading this table, and an earlier version
of this document claimed it was — that `--help` therefore documented the file.
It does not, and on this machine it contradicts it: `avellcc lightbar --help`
prints the **ITE 8911** vocabulary (`--brightness (0-4)`, effects `breathe,
change-color, color-wave, granular`), none of which applies to an ITE 8233,
where brightness is 0-100 and the effects are the six listed above.
`theme.color_key` maps to `--theme-key`, not to a same-named flag, and
`theme.enabled` and `pulse.enabled` have no flag at all. Use
`avellcc lightbar config keys` for the real list.

```bash
systemctl --user stop avellcc-pulse
avellcc lightbar --pulse --pulse-fps 60 --pulse-min-brightness 25 --pulse-debug
```

**Editing it is safe against itself.** Three things make an in-place edit of a
hand-editable file survivable, and the first version had none of them. An audit
reproduced the consequences: 200 concurrent `config set` runs left 29 files
unloadable and silently dropped 78 edits, and a reader racing a writer saw a
zero-byte file 6493 times in two seconds — which is valid TOML that loads as
"every default applies", with no error anywhere.

- **An exclusive lock** around the whole read-modify-write.
- **An atomic replace**: a temp file, fsynced, then renamed. A reader sees
  either the old file or the new one, never a truncated one.
- **Verification before committing.** The line scanner is not a TOML parser and
  never will be. Instead of teaching it every construct, the candidate text is
  decoded and compared against the settings the edit intended; a scanner
  mistake becomes a refusal rather than a wrong write. That is what turns
  `[pulse]  # music` — which used to make a write to `theme.enabled` land on
  `pulse.enabled`, silently — into an error.

**There is a way back from a broken file.** `config set` validates by loading
first, so a file that does not load could not be repaired by the very command
that exists to avoid hand-editing it. `avellcc lightbar config reset` rewrites
the commented defaults and keeps the old file as a `.bak`.

**A misspelled key is an error, not a shrug.** A settings file is edited by
hand, so `min_brigthness` silently doing nothing is the worst possible
outcome. `toml.MetaData.Undecoded()` catches it and the error names the key.
Values are validated on load too, once, with the file named — rather than
surfacing later as a confusing HID error.

```console
$ avellcc lightbar --show-config
Error: /home/disney/.config/avellcc/lightbar.toml: unknown setting pulse.min_brigthness
```

The hook is the exception: it swallows this, because a theme switch must never
fail because of the light bar. `--show-config` is where a bad file shows up.

### The daemon reloads it, and says what it cannot reload

The settings are re-read about once a second, both while playing and while
idle. What happens then depends on which half moved:

| Changed | Effect |
|---|---|
| `min_brightness`, `max_brightness`, `gain` | applied on the next frame |
| `pulse.enabled = false` | the bar goes back to the theme colour |
| `fps`, `input_method`, `input_source` | cava is restarted; the bar stays lit, no flash |
| `player` | logged as needing `systemctl --user restart avellcc-pulse.service` |

That last row is the one worth spelling out. The D-Bus match rule is baked into
the `dbus-monitor` subprocess at start, so a later edit cannot move it. The
daemon compares against **the watcher's actual target**, not against the last
value it read from the file — comparing against the latter made a reverted edit
report the intermediate value as the running one.

A half-saved file — normal while someone is editing — is reported once and the
previous settings are kept, rather than the daemon falling back to defaults
mid-track.

### The bar widget

`omarchy/plugins/disney.lightbar/` is a third-party bar widget for the Omarchy
shell: an icon in the status bar and a panel behind it holding every setting,
plus the three immediate actions that are not settings at all.

The icon is not a glyph. It is a small bar of light painted in the colour the
light bar is actually showing, dimmed by its brightness, breathing while the
pulse daemon owns it. A glyph would say "light bar"; this says what it is doing,
which is the only thing a status bar has room to say.

**The panel never touches the TOML file.** Editing TOML from QML would mean
reimplementing the schema, the ranges and the comment handling a second time,
in a language where none of it is tested. Every read is
`avellcc lightbar config show --json` and every write is
`avellcc lightbar config set <key> <value>`, so validation stays in one place —
a value the panel cannot produce is a value the CLI would have rejected anyway.

That is what `avellcc lightbar config` exists for:

```bash
avellcc lightbar config show [--json]   # settings, palette, and bar state
avellcc lightbar config get <key>
avellcc lightbar config set <key> <value>
avellcc lightbar config keys
avellcc lightbar config path
```

Five details in the QML that are not obvious and each cost something:

- **The root item must declare `implicitWidth`/`implicitHeight`.** The bar
  measures a widget's slot from them (`Bar.qml`: `implicitWidth:
  activeItem.implicitWidth`), and `anchors.fill: parent` on the button does not
  propagate a size back up. Without

  ```qml
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight
  ```

  the slot is zero wide: the plugin is enabled, mounts, runs its subprocesses,
  logs nothing, and paints no pixels. Every first-party panel carries those two
  lines. This is the failure mode to check first when a widget is "installed but
  not there", because a zero-width item is perfectly legal and neither `qmllint`
  nor the shell log has anything to say about it.
- **Commands run by absolute path.** The compositor's PATH is not the
  interactive shell's, and a command missing from it fails *silently* inside
  Quickshell — indistinguishable from a control that does nothing.
- **Sliders commit on release, not while dragging.** Each commit is a
  subprocess that rewrites the settings file, and the daemon re-reads that file
  once a second.
- **Writes are queued, one at a time.** Two `config set` calls racing would each
  read the file, edit their own key, and write the whole thing back; the later
  write would drop the earlier edit.
- **`property string state` is a trap.** `QQuickItem` already has `state` — its
  state-machine property. Shadowing it with a string is legal and quietly
  wrong; `qmllint` catches it, which is why it is worth running before ever
  restarting the shell:

  ```bash
  mkdir -p /tmp/qmlroot && ln -sfn /usr/share/omarchy/shell /tmp/qmlroot/qs
  /usr/lib/qt6/bin/qmllint -I /tmp/qmlroot omarchy/plugins/disney.lightbar/*.qml
  ```

Installing it needs a **full shell restart**, not a rescan:

```bash
cp -r omarchy/plugins/disney.lightbar ~/.config/omarchy/plugins/
omarchy-shell shell rescanPlugins      # discovers the manifest
omarchy plugin enable disney.lightbar right
omarchy-restart-shell                  # the only thing that loads the QML
```

`rescanPlugins` registers the plugin and reports reloading it, but Quickshell
serves the QML it already compiled. Every check that looks at the *disk* — the
plugin exists, shell.json lists it, the file has the right lines — passes while
the screen still shows the old thing. The assertion that catches it compares the
QML's mtime against the `quickshell` process start time, because "I reloaded it"
is a claim about the process, not about the file.

### A theme switch mid-track

The daemon re-reads `colors.toml` once a second, not once a frame, and eases
into the new colours without resetting its smoothing state. The hook's own
static write lands and is overwritten within a frame — and then restored
exactly, because the daemon restores from the state file that write updates.

## Restoring at login and after resume

The ITE 8291 loses its LED state across suspend, and systemd's **user** manager
has no `sleep.target`, so a user unit cannot simply order itself after resume.

`avellcc-keyboard.service` therefore watches logind's `PrepareForSleep` signal on
the system bus, mirroring what Omarchy's own `omarchy-system-sleep-monitor`
does, and re-runs `avellcc reload`. That covers the light bar too: `reload`
restores whichever controller wrote the saved state. Whether the ITE 8233 also
loses its state across suspend has not been measured here — tuxedo-drivers
rewrites the bar's colour on every resume, which implies it does — but the
restore costs two HID packets either way. The signal carries `true` just before
suspending and `false` on resume; only the resume edge acts, after a short delay
that lets the USB device re-enumerate.

Running in the user session avoids a root-owned
`/usr/lib/systemd/system-sleep` hook. The same script also reloads once at
start, which covers login and any restart of the service.

The parsing is exercisable without suspending the machine:

```bash
printf 'signal …member=PrepareForSleep\n   boolean true\nsignal …\n   boolean false\n' \
  | AVELLCC_BIN=/path/to/stub AVELLCC_RESUME_DELAY=0 \
    ~/.local/bin/avellcc-resume-monitor --consume-stdin
```

Exactly one reload should fire, on the `false` edge.

## udev

```
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="048d", ATTRS{idProduct}=="600b", \
    GROUP="input", MODE="0660", TAG+="uaccess"
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="048d", ATTRS{idProduct}=="7001", \
    GROUP="input", MODE="0660", TAG+="uaccess"
```

`GROUP="input"` is what actually grants access. `TAG+="uaccess"` alone applies
an ACL only for a logged-in seat user, and only on an `add` uevent — a
`udevadm trigger` without `--action=add` sets the mode but never fires it, which
looks exactly like the rule not matching. System services get no uaccess ACL at
all, so the group is what makes a systemd unit work.

## Files

| Path | Purpose |
|---|---|
| `omarchy/50-avellcc-keyboard` | the keyboard theme-set hook |
| `omarchy/51-avellcc-lightbar` | the light bar theme-set hook |
| `omarchy/avellcc-pulse.service` | user unit for the music pulse |
| `internal/config/lightbar_settings.go` | the settings file, its defaults and validation |
| `internal/config/lightbar_settings_set.go` | the comment-preserving writer behind `config set` |
| `omarchy/plugins/disney.lightbar/` | the bar widget and its panel |
| `omarchy/avellcc-resume-monitor` | login and resume restore |
| `omarchy/avellcc-keyboard.service` | user unit that runs the monitor |
| `udev/60-avellcc-storm470.rules` | hidraw access |

## Install

```bash
omarchy hook install theme-set omarchy/50-avellcc-keyboard
omarchy hook install theme-set omarchy/51-avellcc-lightbar
install -Dm755 omarchy/avellcc-resume-monitor ~/.local/bin/avellcc-resume-monitor
install -Dm644 omarchy/avellcc-keyboard.service ~/.config/systemd/user/avellcc-keyboard.service
systemctl --user daemon-reload && systemctl --user enable --now avellcc-keyboard.service
```

The pulse is opt-in and needs cava:

```bash
omarchy pkg add cava
install -Dm644 omarchy/avellcc-pulse.service ~/.config/systemd/user/avellcc-pulse.service
systemctl --user daemon-reload && systemctl --user enable --now avellcc-pulse.service
```
