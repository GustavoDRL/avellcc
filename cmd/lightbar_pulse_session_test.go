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
	// This cava emits one frame and then goes silent forever.
	fakeCava(t, "#!/bin/sh\nprintf '\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0\\0'\nsleep 3600\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	playing := make(chan bool, 4)
	isPlaying := true
	settings := defaultTestSettings(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The first frame fails the write on /dev/null, so send the stop first
		// and let the session pick it up without another frame.
		playing <- false
		_, _ = runCavaSession(ctx, &cobra.Command{}, nullController(t),
			testMapper(), playing, &isPlaying, &settings, "org.mpris.MediaPlayer2.test")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end after playback stopped; it is waiting for a frame")
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
