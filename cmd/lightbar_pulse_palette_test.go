package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// G05: the idle loop re-read the settings file and never the theme.
//
// The daemon starts at login, before Omarchy has published the applied theme,
// so awaitPalette answers with whatever colors.toml is on disk at that instant
// — the *previous* theme's. Measured on this machine: the daemon logged
// "bass #89b4fa, mid #f9e2af, treble #f5c2e7" (Catppuccin) while the applied
// theme resolved to #8aa4b0/#ff4848/#6e8f7a, and stayed that way, because the
// only place the palette was re-read was inside a cava session. A daemon whose
// music never plays never gets there.
func TestIdleRefreshPicksUpTheAppliedTheme(t *testing.T) {
	withAppliedTheme(t, "")

	// The palette the daemon came up with: the theme that was applied before
	// this one.
	mapper := testMapper()
	stale := mapper.Palette()

	settings := defaultTestSettings(t)
	settings = pulseIdleRefresh(&cobra.Command{}, settings, "org.mpris.MediaPlayer2.test", mapper)

	got := mapper.Palette()
	if got == stale {
		t.Fatalf("the idle loop left the daemon on %s/%s/%s; the applied theme is a "+
			"different one and only a cava session would have noticed",
			stale.Bass.Hex(), stale.Mid.Hex(), stale.Treble.Hex())
	}
	want, err := omarchy.CurrentPalette()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("palette = %s/%s/%s, want the applied theme's %s/%s/%s",
			got.Bass.Hex(), got.Mid.Hex(), got.Treble.Hex(),
			want.Bass.Hex(), want.Mid.Hex(), want.Treble.Hex())
	}
	// The settings half must keep working: this is the same call.
	if !settings.Pulse.Enabled {
		t.Error("the settings were lost by the refresh")
	}
}

// A theme switch that happens while nothing is playing has to reach the daemon
// too — that is the same defect from the other end.
func TestIdleRefreshFollowsAThemeSwitch(t *testing.T) {
	withAppliedTheme(t, "")

	mapper := testMapper()
	settings := defaultTestSettings(t)
	settings = pulseIdleRefresh(&cobra.Command{}, settings, "org.mpris.MediaPlayer2.test", mapper)
	first := mapper.Palette()

	// `omarchy theme set` replaces this file.
	colors := filepath.Join(os.Getenv("HOME"), ".local", "state", "omarchy",
		"current", "theme", "colors.toml")
	if err := os.WriteFile(colors, []byte("accent = \"#00FF00\"\nred = \"#FF0000\"\n"+
		"blue = \"#0000FF\"\ncyan = \"#00FFFF\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pulseIdleRefresh(&cobra.Command{}, settings, "org.mpris.MediaPlayer2.test", mapper)
	if got := mapper.Palette(); got == first {
		t.Errorf("the daemon stayed on %s after the theme was switched", first.Bass.Hex())
	}
	if got := mapper.Palette().Bass.Hex(); got != "#00ff00" {
		t.Errorf("bass = %s, want the new theme's accent #00ff00", got)
	}
}

// An unreadable theme leaves the colours in force rather than blanking them:
// `omarchy theme set` replaces colors.toml, and the bar must not flash for the
// instant it is missing.
func TestRefreshKeepsThePaletteWhenTheThemeCannotBeRead(t *testing.T) {
	withAppliedTheme(t, "")
	mapper := testMapper()
	refreshPalette(mapper)
	applied := mapper.Palette()

	colors := filepath.Join(os.Getenv("HOME"), ".local", "state", "omarchy",
		"current", "theme", "colors.toml")
	if err := os.Remove(colors); err != nil {
		t.Fatal(err)
	}
	refreshPalette(mapper)
	if got := mapper.Palette(); got != applied {
		t.Errorf("palette = %s after the theme file vanished, want the last good %s",
			got.Bass.Hex(), applied.Bass.Hex())
	}
}
