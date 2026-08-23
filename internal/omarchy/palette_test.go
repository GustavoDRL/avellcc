package omarchy

import (
	"os"
	"path/filepath"
	"testing"
)

const stockThemes = "/usr/share/omarchy/themes"

// The mid colours here are the ones the awk picker in the theme-set hook
// already produces, copied from docs/omarchy-integration.md. Asserting them
// against the real theme files is what keeps the Go port and the hook from
// drifting apart.
func TestPaletteMatchesHookPicksOnStockThemes(t *testing.T) {
	cases := map[string]string{
		"tokyo-night": "#e0af68",
		"everforest":  "#e67e80",
		"gruvbox":     "#d3869b",
		"retro-82":    "#028391",
		"catppuccin":  "#f9e2af",
	}

	for theme, wantMid := range cases {
		path := filepath.Join(stockThemes, theme, "colors.toml")
		if _, err := os.Stat(path); err != nil {
			t.Skipf("stock themes not installed: %v", err)
		}
		colors, err := ReadColors(path)
		if err != nil {
			t.Fatalf("%s: %v", theme, err)
		}
		p := PaletteFrom(colors)
		if got := p.Mid.Hex(); got != wantMid {
			t.Errorf("%s: mid = %s, want %s (the hook's pick)", theme, got, wantMid)
		}
		if p.Treble == p.Bass || p.Treble == p.Mid {
			t.Errorf("%s: treble %s collapses onto bass %s / mid %s",
				theme, p.Treble.Hex(), p.Bass.Hex(), p.Mid.Hex())
		}
	}
}

// A theme with no hue anywhere has no contrast to offer, and saying so by
// repeating the accent is better than picking a near-grey at random.
func TestMonochromeThemeCollapsesToAccent(t *testing.T) {
	path := filepath.Join(stockThemes, "vantablack", "colors.toml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("stock themes not installed: %v", err)
	}
	colors, err := ReadColors(path)
	if err != nil {
		t.Fatal(err)
	}
	p := PaletteFrom(colors)
	if p.Mid != p.Bass || p.Treble != p.Bass {
		t.Errorf("monochrome theme should collapse to the accent, got bass=%s mid=%s treble=%s",
			p.Bass.Hex(), p.Mid.Hex(), p.Treble.Hex())
	}
}

// The accent is the one key the picker cannot do without.
func TestReadColorsRequiresAccent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colors.toml")
	if err := os.WriteFile(path, []byte("mode = \"dark\"\nred = \"#ff0000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadColors(path); err == nil {
		t.Error("expected an error for a colors.toml with no accent")
	}
}

// Keys that are not six-digit colours share the file with the ones that are.
func TestReadColorsIgnoresNonColorKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colors.toml")
	body := "mode = \"dark\"\naccent = \"#89b4fa\"\nfont = \"CaskaydiaMono\"\nred = \"f38ba8\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	colors, err := ReadColors(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(colors) != 2 {
		t.Errorf("parsed %d colours, want 2: %v", len(colors), colors)
	}
	if colors["red"].Hex() != "#f38ba8" {
		t.Errorf("unquoted, unprefixed colour not parsed: %v", colors["red"])
	}
}

// Sweeping every installed theme, rather than the five in the table, is what
// found this: `solitude` has exactly one candidate that survives the
// saturation and value floors, and it was handed to both mid and treble. Two
// of the three bands then painted the same colour and the bar could not tell
// them apart.
func TestNoInstalledThemeCollapsesTwoBandsOntoOneColour(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(stockThemes, "*", "colors.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("stock themes not installed")
	}
	if home, err := os.UserHomeDir(); err == nil {
		user, _ := filepath.Glob(filepath.Join(home, ".config", "omarchy", "themes", "*", "colors.toml"))
		paths = append(paths, user...)
	}

	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		colors, err := ReadColors(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		p := PaletteFrom(colors)

		// A fully monochrome theme legitimately has one colour, and saying so
		// by repeating the accent everywhere is the honest answer.
		if p.Bass == p.Mid && p.Mid == p.Treble {
			continue
		}
		if p.Mid == p.Treble {
			t.Errorf("%s: mid and treble are both %s", name, p.Mid.Hex())
		}
	}
}
