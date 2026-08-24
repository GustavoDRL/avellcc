package cmd

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
	"github.com/hugo-andrade/avellcc/internal/omarchy"
	"github.com/hugo-andrade/avellcc/internal/pulse"
)

// The pulse daemon paints the chassis bar in time with what is playing, in the
// current Omarchy theme's own colours.
//
// It owns the bar only while the player is actually playing. On pause, on
// stop, and on exit it hands the bar back to the saved state, which is what
// the theme-set hook writes — so the theme stays the resting appearance and
// the music is the exception, not the other way round.
//
// The spectrum comes from cava rather than from an FFT written here. cava
// already solves PipeWire capture, windowing, per-bar frequency distribution
// and perceptual smoothing, and its raw output mode is a stable interface.
// Reading it also paces the loop for free: cava emits at its configured frame
// rate, so there is no timer here to drift against it.

const (
	defaultPulsePlayer = "spotify"
	mprisPath          = "/org/mpris/MediaPlayer2"
	mprisPlayerIface   = "org.mpris.MediaPlayer2.Player"

	// How often the daemon re-reads its settings and the applied theme. Both
	// change a few times a day at most, so once a second is already generous,
	// and doing it per frame would be thirty file reads a second.
	pulseReloadInterval = time.Second
)

// runLightbarPulse is the daemon entry point. It returns only on error or when
// the context is cancelled.
func runLightbarPulse(cmd *cobra.Command, ctrl *lightbar.ITE8233) error {
	if _, err := exec.LookPath("cava"); err != nil {
		return fmt.Errorf("cava is required for --pulse and is not on PATH " +
			"(install it with: omarchy pkg add cava)")
	}
	if _, err := exec.LookPath("dbus-monitor"); err != nil {
		return fmt.Errorf("dbus-monitor is required for --pulse and is not on PATH")
	}

	// A fresh install has nothing to edit until something writes it. Doing it
	// here means the file exists by the time anyone goes looking for it, and
	// it is written once ever, so later edits survive every upgrade.
	if wrote, err := config.WriteDefaultLightbarSettingsFile(); err != nil {
		fmt.Fprintf(os.Stderr, "pulse: could not write default settings: %v\n", err)
	} else if wrote {
		fmt.Fprintf(os.Stderr, "pulse: wrote default settings to %s\n", config.LightbarSettingsPath())
	}

	settings, err := effectiveLightbarSettings(cmd)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The unit is not ordered after the compositor, so at login this can run
	// before Omarchy has published the applied theme. Failing outright made
	// systemd restart the daemon every five seconds until the state appeared;
	// waiting is both quieter and honest about what it is waiting for.
	palette, err := awaitPalette(ctx)
	if err != nil {
		return err
	}
	mapper := pulse.New(pulseMapperConfig(settings.Pulse), palette)

	// Fixed for the life of the process: the D-Bus match rule is baked into
	// the subprocess started below, so a later edit to pulse.player cannot
	// move it. Everything downstream compares against this, not against
	// whatever the file most recently said.
	player := settings.Pulse.MPRISName()
	fmt.Fprintf(os.Stderr, "pulse: watching %s at %d fps; bass %s, mid %s, treble %s\n",
		player, settings.Pulse.FPS, palette.Bass.Hex(), palette.Mid.Hex(), palette.Treble.Hex())

	playing := watchPlayer(ctx, player)

	// The bar is only ours while the music plays; put it back on every exit.
	defer restoreSavedLightbar()

	// A write failure that never clears used to spin forever: the loop treated
	// every error as recoverable, re-entered with the same dead descriptor, and
	// because it never returned, Restart=always never fired either.
	consecutiveFailures := 0

	isPlaying := false
	for ctx.Err() == nil {
		settings = pulseIdleRefresh(cmd, settings, player, mapper)

		if !isPlaying || !settings.Pulse.Enabled {
			select {
			case <-ctx.Done():
				return nil
			case isPlaying = <-playing:
			case <-time.After(2 * time.Second):
			}
			continue
		}

		fmt.Fprintln(os.Stderr, "pulse: playback started; the bar follows the music")
		restart, err := runCavaSession(ctx, cmd, ctrl, mapper, playing, &isPlaying, &settings, player)
		if err != nil {
			consecutiveFailures++
			fmt.Fprintf(os.Stderr, "pulse: %v\n", err)

			// A suspend/resume can re-enumerate the USB device, which leaves
			// the descriptor open and every ioctl on it failing. Nothing else
			// recovers from that, because the device is opened once at start.
			if errors.Is(err, errLightbarWrite) {
				if rerr := ctrl.Reopen(); rerr != nil {
					fmt.Fprintf(os.Stderr, "pulse: could not reopen the lightbar: %v\n", rerr)
				} else {
					fmt.Fprintf(os.Stderr, "pulse: reopened the lightbar at %s\n", ctrl.Path())
				}
			}

			// Give up rather than loop forever. Exiting non-zero is what lets
			// the unit's Restart=always do its job; looping here guaranteed it
			// never could.
			if consecutiveFailures >= maxPulseFailures {
				return fmt.Errorf("giving up after %d consecutive failures: %w",
					consecutiveFailures, err)
			}

			backoff := time.Duration(1<<min(consecutiveFailures-1, 5)) * 2 * time.Second
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}

		consecutiveFailures = 0
		// A restart for changed capture settings keeps the bar lit; only a real
		// stop hands it back, so editing the file mid-track does not flash.
		if !restart {
			restoreSavedLightbar()
			fmt.Fprintln(os.Stderr, "pulse: playback stopped; the bar is back on the theme colour")
		}
	}
	return nil
}

// maxPulseFailures is how many consecutive session failures are tolerated
// before the daemon exits and lets systemd restart it from a clean state.
const maxPulseFailures = 8

// errLightbarWrite marks the failures a reopen might fix, so a cava problem
// does not trigger a pointless device reopen.
var errLightbarWrite = errors.New("lightbar write failed")

// awaitPalette waits for Omarchy to publish a theme rather than treating its
// absence as fatal.
func awaitPalette(ctx context.Context) (omarchy.Palette, error) {
	warned := false
	for {
		palette, err := omarchy.CurrentPalette()
		if err == nil {
			return palette, nil
		}
		if !warned {
			fmt.Fprintf(os.Stderr, "pulse: waiting for the applied theme: %v\n", err)
			warned = true
		}
		select {
		case <-ctx.Done():
			return omarchy.Palette{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// pulseIdleRefresh re-reads everything that can change while nothing is
// playing: the settings file *and* the applied theme.
//
// The palette used to be re-read only inside a cava session, which meant a
// daemon that had never played a note kept whatever palette it started with,
// forever. That is not hypothetical: this daemon starts at login, before
// Omarchy publishes the theme, so awaitPalette answers with the *previous*
// theme's colours — it covers "there is no theme yet", not "there is one, but
// it is the old one". Measured on this machine: the daemon was pulsing
// Catppuccin's #89b4fa/#f9e2af/#f5c2e7 while the applied theme resolved to
// #8aa4b0/#ff4848/#6e8f7a, twelve seconds apart at boot and hours later still.
func pulseIdleRefresh(cmd *cobra.Command, settings config.LightbarSettings,
	player string, mapper *pulse.Mapper) config.LightbarSettings {

	// Settings are re-read here as well as inside a session, so a file edit
	// made while nothing is playing is not held until the next track.
	if reloaded, err := effectiveLightbarSettings(cmd); err != nil {
		// A half-saved file is normal while someone is editing it. Say so
		// once and keep the settings that were already working.
		fmt.Fprintf(os.Stderr, "pulse: keeping the previous settings: %v\n", err)
	} else if reloaded != settings {
		settings = announceSettingsChange(settings, reloaded, player, mapper)
	}
	refreshPalette(mapper)
	return settings
}

// refreshPalette re-reads the applied theme and moves the mapper onto it when
// it has changed. An unreadable theme leaves the colours in force: a theme
// switch replaces colors.toml, and losing the palette for the instant that file
// is missing would flash the bar.
func refreshPalette(mapper *pulse.Mapper) {
	p, err := omarchy.CurrentPalette()
	if err != nil || p == mapper.Palette() {
		return
	}
	mapper.SetPalette(p)
	fmt.Fprintf(os.Stderr, "pulse: theme changed; bass %s, mid %s, treble %s\n",
		p.Bass.Hex(), p.Mid.Hex(), p.Treble.Hex())
}

// announceSettingsChange applies what can be applied in place and says what
// cannot, rather than leaving a setting looking as though it did nothing.
func announceSettingsChange(old, new config.LightbarSettings, player string,
	mapper *pulse.Mapper) config.LightbarSettings {

	mapper.SetConfig(pulseMapperConfig(new.Pulse))
	fmt.Fprintf(os.Stderr, "pulse: settings reloaded from %s\n", config.LightbarSettingsPath())

	if new.Pulse.MPRISName() != player {
		fmt.Fprintf(os.Stderr, "pulse: player is now %q, but the running daemon still follows %s "+
			"— run: systemctl --user restart avellcc-pulse.service\n",
			new.Pulse.Player, player)
	}
	if !new.Pulse.Enabled && old.Pulse.Enabled {
		fmt.Fprintln(os.Stderr, "pulse: disabled in the settings; the bar stays on the theme colour")
	}
	return new
}

// captureChanged reports whether a setting that cava owns has moved, which can
// only take effect by restarting the capture.
func captureChanged(a, b config.PulseSettings) bool {
	return a.FPS != b.FPS || a.InputMethod != b.InputMethod || a.InputSource != b.InputSource
}

// runCavaSession drives the bar for as long as playback lasts. It reports
// whether it stopped only to pick up new capture settings, in which case the
// caller should come straight back rather than hand the bar over.
func runCavaSession(ctx context.Context, cmd *cobra.Command, ctrl *lightbar.ITE8233,
	mapper *pulse.Mapper, playing <-chan bool, isPlaying *bool,
	settings *config.LightbarSettings, player string) (bool, error) {

	confPath, err := writeCavaConfig(settings.Pulse)
	if err != nil {
		return false, err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cava := exec.CommandContext(sessionCtx, "cava", "-p", confPath)
	cava.Stderr = os.Stderr
	stdout, err := cava.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cava.Start(); err != nil {
		return false, fmt.Errorf("starting cava: %w", err)
	}
	// cava writes continuously; without draining the pipe on the way out it
	// blocks on write and Wait never returns.
	defer func() {
		cancel()
		_, _ = io.Copy(io.Discard, stdout)
		_ = cava.Wait()
	}()

	// Playback state arrives on its own goroutine, so the frame loop can stay
	// a plain blocking read of cava's stdout.
	//
	// Two things here are load-bearing, and the first version had neither.
	//
	// The goroutine records what it saw in a local and the caller reads it only
	// after joining, because close(stopped) is the sole happens-before edge to
	// it. Writing through the caller's pointer was a data race the detector
	// confirms: four of the five ways this function returns leave the goroutine
	// live, so a dead session could set the flag the *next* session reads.
	//
	// And it cancels the session when playback stops, which is what unblocks
	// the frame loop's io.ReadFull. Without that, a pause is only noticed when
	// the next frame arrives — so pausing while PipeWire has suspended a silent
	// sink left the bar frozen on its last frame for the whole pause.
	stopped := make(chan struct{})
	var observed bool
	var observedSet bool
	go func() {
		defer close(stopped)
		for {
			select {
			case <-sessionCtx.Done():
				return
			case p := <-playing:
				observed, observedSet = p, true
				if !p {
					cancel()
					return
				}
			}
		}
	}()
	// Joining also guarantees this goroutine is gone before the caller can
	// start the next session's. Two of them receiving from `playing` at once
	// let a pause be consumed by the dead one and lost.
	defer func() {
		cancel()
		<-stopped
		if observedSet {
			*isPlaying = observed
		}
	}()

	reader := bufio.NewReader(stdout)
	bars := make([]uint16, pulse.Bands)
	buf := make([]byte, pulse.Bands*2)
	reload := time.Now()
	debugTick := time.Now()
	frames, peak := 0, 0

	for {
		if sessionCtx.Err() != nil {
			return false, nil
		}

		if _, err := io.ReadFull(reader, buf); err != nil {
			// A cancelled session kills cava, so EOF here is the expected end
			// of a normal stop — reporting it as a cava failure put a wrong
			// error in the journal on every clean shutdown.
			if sessionCtx.Err() != nil {
				return false, nil
			}
			return false, fmt.Errorf("cava stopped producing frames: %w", err)
		}
		for i := range bars {
			bars[i] = binary.LittleEndian.Uint16(buf[i*2:])
		}

		color, brightness := mapper.Frame(bars)
		if err := ctrl.SetColor(color[0], color[1], color[2], brightness); err != nil {
			return false, fmt.Errorf("%w: %v", errLightbarWrite, err)
		}
		frames++
		if brightness > peak {
			peak = brightness
		}

		// The bar is the real output and only a person can see it, so the
		// debug line exists to be read next to it: it says what was written,
		// not what should have been. It reports the interval's *peak*
		// brightness rather than whatever the last frame happened to be —
		// sampling one frame in thirty misses every beat it lands between.
		if pulseDebug && time.Since(debugTick) >= time.Second {
			fmt.Fprintf(os.Stderr, "pulse: %2d fps  bars %v  band %-6s  %s  brightness %3d (peak %3d)\n",
				frames, bars, mapper.Dominant(), color.Hex(), brightness, peak)
			debugTick, frames, peak = time.Now(), 0, 0
		}

		if time.Since(reload) < pulseReloadInterval {
			continue
		}
		reload = time.Now()

		refreshPalette(mapper)

		reloaded, err := effectiveLightbarSettings(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pulse: keeping the previous settings: %v\n", err)
			continue
		}
		if reloaded == *settings {
			continue
		}
		needsRestart := captureChanged(settings.Pulse, reloaded.Pulse)
		*settings = announceSettingsChange(*settings, reloaded, player, mapper)
		if !reloaded.Pulse.Enabled {
			return false, nil
		}
		if needsRestart {
			fmt.Fprintln(os.Stderr, "pulse: restarting the capture for the new settings")
			return true, nil
		}
	}
}

// restoreSavedLightbar puts back whatever the theme-set hook last wrote.
func restoreSavedLightbar() {
	if err := restoreLightbar8233State(loadLightbar8233State()); err != nil {
		fmt.Fprintf(os.Stderr, "pulse: could not restore the saved lightbar state: %v\n", err)
	}
}

// writeCavaConfig regenerates the cava config from the settings in force. It
// is generated rather than shipped because the bar count and frame rate have
// to agree with the reader on the other end of the pipe; an edited copy that
// disagreed would desynchronise the frames rather than fail loudly. The file a
// user edits is lightbar.toml.
func writeCavaConfig(p config.PulseSettings) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	dir = filepath.Join(dir, "avellcc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cava-pulse.conf")

	conf := fmt.Sprintf(`# Generated from %s on every capture start — edits here are overwritten.
# The bar count and frame rate must match what the daemon reads.
[general]
framerate = %d
bars = %d
autosens = 1

[input]
method = %s
source = %s

[output]
method = raw
raw_target = /dev/stdout
data_format = binary
bit_format = 16bit
channels = mono
`, config.LightbarSettingsPath(), p.FPS, pulse.Bands, p.InputMethod, p.InputSource)

	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// watchPlayer reports the player's PlaybackStatus, starting with its current
// value and then every change.
//
// This shells out to dbus-monitor rather than linking a D-Bus library, which
// is the same choice avellcc-resume-monitor already makes for logind's
// PrepareForSleep: one long-lived subprocess, no new dependency, and the
// parsing stays a pure function that a test can drive from a fixture.
func watchPlayer(ctx context.Context, base string) <-chan bool {
	out := make(chan bool, 4)

	send := func(playing bool) {
		select {
		case out <- playing:
		case <-ctx.Done():
		}
	}

	go func() {
		for ctx.Err() == nil {
			dest := resolvePlayerDest(ctx, base)

			// Asked on every bind, not only the first: rebinding to another
			// instance, or recovering from a dead dbus-monitor, both leave a
			// window in which a status change carries no signal to read.
			send(queryPlaying(ctx, dest))

			// A match rule naming a well-known sender is resolved by the bus,
			// so this works whether or not the player is running yet. The
			// NameOwnerChanged rule matches the whole namespace so that an
			// instance-qualified player is caught the moment it appears.
			cmd := exec.CommandContext(ctx, "dbus-monitor", "--session",
				fmt.Sprintf("type='signal',sender='%s',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'", dest),
				fmt.Sprintf("type='signal',interface='org.freedesktop.DBus',member='NameOwnerChanged',arg0namespace='%s'", base))
			stdout, err := cmd.StdoutPipe()
			if err == nil && cmd.Start() == nil {
				_ = scanPlayerSignals(stdout,
					func(status string) { send(status == "Playing") },
					// The player appearing or disappearing does not carry the
					// status, so ask for it rather than guess. A different
					// instance than the one this dbus-monitor is bound to
					// means the rule is now watching a dead name: end the
					// scan so the loop rebinds to the live one.
					func() {
						if next := resolvePlayerDest(ctx, base); next != dest {
							_ = cmd.Process.Kill()
							return
						}
						send(queryPlaying(ctx, dest))
					})
				_ = cmd.Wait()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()

	return out
}

// scanPlayerSignals parses dbus-monitor's text output. PropertiesChanged
// prints the changed property name on one line and its value on the next:
//
//	string "PlaybackStatus"
//	variant             string "Playing"
func scanPlayerSignals(r io.Reader, onStatus func(string), onOwnerChange func()) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	awaitValue := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if awaitValue {
			awaitValue = false
			if strings.HasPrefix(line, "variant") {
				if v := lastQuoted(line); v != "" {
					onStatus(v)
				}
				continue
			}
		}

		switch {
		case strings.Contains(line, "member=NameOwnerChanged"):
			onOwnerChange()
		case line == `string "PlaybackStatus"`:
			awaitValue = true
		}
	}
	return scanner.Err()
}

// queryPlaying reads the player's current status. A player that is not running
// owns no bus name, and the call failing is the answer rather than an error.
// resolvePlayerDest returns the bus name to watch for base, which is the name
// `pulse.player` expands to. A player that qualifies its bus name with an
// instance suffix never owns the bare name — Omarchy Spotify's backend
// publishes org.mpris.MediaPlayer2.OmarchySpotify.instance<pid>, because
// mpris-server requires each instance to be unique — so fall back to the
// live name inside base's namespace. The bare name wins when it exists, which
// keeps a plain player like Spotify on the exact name it owns.
func resolvePlayerDest(ctx context.Context, base string) string {
	return pickPlayerDest(base, sessionBusNames(ctx))
}

// pickPlayerDest is resolvePlayerDest's choice, split out so a test can drive
// it from a name list instead of a live bus.
func pickPlayerDest(base string, names []string) string {
	prefix := base + "."
	best := ""
	for _, name := range names {
		if name == base {
			return base
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// Deterministic when two instances overlap during a restart: the
		// lowest name is the same choice on every poll, so the watcher does
		// not flap between them.
		if best == "" || name < best {
			best = name
		}
	}
	if best == "" {
		return base
	}
	return best
}

// sessionBusNames lists the names currently acquired on the session bus.
// ListNames is asked for rather than `busctl list`, which also reports
// activatable names that nobody owns yet.
func sessionBusNames(ctx context.Context) []string {
	cmd := exec.CommandContext(ctx, "busctl", "--user", "--no-pager", "call",
		"org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "ListNames")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseBusNames(string(out))
}

// parseBusNames pulls the names out of busctl's `as <count> "a" "b" ...`
// rendering of ListNames.
func parseBusNames(out string) []string {
	var names []string
	rest := out
	for {
		start := strings.IndexByte(rest, '"')
		if start < 0 {
			return names
		}
		rest = rest[start+1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return names
		}
		names = append(names, rest[:end])
		rest = rest[end+1:]
	}
}

func queryPlaying(ctx context.Context, dest string) bool {
	cmd := exec.CommandContext(ctx, "busctl", "--user", "get-property",
		dest, mprisPath, mprisPlayerIface, "PlaybackStatus")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return lastQuoted(string(out)) == "Playing"
}

// lastQuoted returns the contents of the final double-quoted run in s.
func lastQuoted(s string) string {
	end := strings.LastIndexByte(s, '"')
	if end <= 0 {
		return ""
	}
	start := strings.LastIndexByte(s[:end], '"')
	if start < 0 {
		return ""
	}
	return s[start+1 : end]
}
