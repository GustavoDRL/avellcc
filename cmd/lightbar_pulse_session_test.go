package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/hidraw"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
	"github.com/hugo-andrade/avellcc/internal/omarchy"
	"github.com/hugo-andrade/avellcc/internal/pulse"
)

// `go test -race ./...` used to pass while proving nothing about the daemon:
// no test called runCavaSession, so the detector never observed the code where
// the race actually lived. These tests exist to put it under the detector.
//
// They need no hardware: cava is a shell script on PATH, and the "lightbar" is
// /dev/null, whose ioctls fail — which is the write-error path.

// fakeCava puts a cava on PATH that streams frames forever, and returns the
// directory so the caller can prepend it.
func fakeCava(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cava")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// nullController is a controller whose device is /dev/null: Open succeeds,
// every feature-report ioctl fails.
func nullController(t *testing.T) *lightbar.ITE8233 {
	t.Helper()
	ctrl := lightbar.NewITE8233(&hidraw.HidrawDevice{Path: os.DevNull}, 0x7001)
	if err := ctrl.Open(); err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })
	return ctrl
}

func testMapper() *pulse.Mapper {
	return pulse.New(pulse.DefaultConfig(), omarchy.Palette{
		Bass:   omarchy.RGB{0x89, 0xb4, 0xfa},
		Mid:    omarchy.RGB{0xf9, 0xe2, 0xaf},
		Treble: omarchy.RGB{0xf5, 0xc2, 0xe7},
	})
}

// The race the detector found: the session goroutine wrote through the
// caller's isPlaying pointer, and four of the five ways runCavaSession returns
// leave that goroutine live. Run with -race.
func TestSessionDoesNotRaceOnPlaybackFlag(t *testing.T) {
	fakeCava(t, "#!/bin/sh\nwhile :; do printf '\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0'; done\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	playing := make(chan bool, 4)
	// A Playing→Playing transition is what NameOwnerChanged re-queries produce,
	// and it is the write that raced with the caller's read.
	playing <- true

	isPlaying := true
	settings := defaultTestSettings(t)

	// /dev/null fails the first HID write, so the session exits by the
	// write-error path — one of the four that used to leave the goroutine live.
	restart, err := runCavaSession(ctx, &cobra.Command{}, nullController(t),
		testMapper(), playing, &isPlaying, &settings, "org.mpris.MediaPlayer2.test")

	if restart {
		t.Error("a write failure should not be reported as a capture restart")
	}
	if err == nil {
		t.Fatal("expected the failing lightbar write to surface as an error")
	}
	// The caller must be able to read isPlaying without the detector firing;
	// the value itself is whatever the goroutine last observed.
	_ = isPlaying
}

// A stop must end the session promptly even when no frame arrives, which is
// what a pause during silence looks like: cava stays alive and quiet.
func TestSessionStopsWithoutWaitingForAFrame(t *testing.T) {
	// A cava that never emits a frame and never exits, which is what a pause
	// during silence gives the daemon. Two details are load-bearing:
	//
	//   - NO frame. An earlier version printed one, and that frame failed the
	//     write on /dev/null — so the session could leave by the write-error
	//     path instead of the stop path, and the test then proved nothing about
	//     stopping. Measured: with the frame, deleting the `cancel()` from the
	//     stop branch of runCavaSession left this test GREEN.
	//   - `exec`, so the quiet cava is one process rather than a shell with a
	//     `sleep` child. exec.CommandContext kills the process it started; an
	//     orphaned `sleep` keeps the stdout pipe open, and the session's cleanup
	//     then blocks in io.Copy for the full hour. Measured directly: the
	//     forking shell took over 40 s (still running when the probe gave up),
	//     `exec` took 301 ms. THAT is what the 5-second deadline this test used
	//     to carry was catching one run in seven — not scheduler jitter, an
	//     orphan holding the pipe.
	fakeCava(t, "#!/bin/sh\nexec sleep 3600\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	playing := make(chan bool, 4)
	isPlaying := true
	settings := defaultTestSettings(t)
	// Buffered and sent before the session starts, so the stop is already
	// waiting when the session's watcher goroutine reaches its first receive.
	playing <- false

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runCavaSession(ctx, &cobra.Command{}, nullController(t),
			testMapper(), playing, &isPlaying, &settings, "org.mpris.MediaPlayer2.test")
	}()

	// The question is CAUSAL, not a stopwatch: did the stop end the session, or
	// did the context expire while it sat waiting for a frame that this cava
	// will not send for an hour? A broken session can only leave runCavaSession
	// when sessionCtx is done, so `ctx.Err() != nil` on the way out is exactly
	// the defect, and a slow machine cannot fake it.
	//
	// The 5-second wall-clock deadline this replaces was measured failing once
	// in seven runs under CPU contention on a healthy build. A red that means
	// "the machine was busy" teaches everyone to ignore red, which is worse
	// than having no test at all.
	select {
	case <-done:
		if ctx.Err() != nil {
			t.Fatal("the session only ended when the context expired; " +
				"it was waiting for a frame instead of acting on the stop")
		}
	case <-time.After(time.Minute):
		// Only reachable if the session ignores its context too, which no
		// amount of load can produce. Present so a hang is a failure rather
		// than a test binary that never returns.
		t.Fatal("the session never ended at all, context deadline included")
	}
	if isPlaying {
		t.Error("the caller's playback flag was not updated by the session")
	}
}

func defaultTestSettings(t *testing.T) config.LightbarSettings {
	t.Helper()
	s, err := effectiveLightbarSettings(&cobra.Command{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
