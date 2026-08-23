package pulse

import (
	"testing"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// The Catppuccin palette, which is what the picker derives for the theme this
// was developed against.
var testPalette = omarchy.Palette{
	Bass:   omarchy.RGB{0x89, 0xb4, 0xfa},
	Mid:    omarchy.RGB{0xf9, 0xe2, 0xaf},
	Treble: omarchy.RGB{0xf5, 0xc2, 0xe7},
}

// frame builds a spectrum frame from three 0-1 band levels.
func frame(bass, mid, treble float64) []uint16 {
	bars := make([]uint16, Bands)
	for i := range bars {
		level := bass
		switch {
		case i >= 2*Bands/3:
			level = treble
		case i >= Bands/3:
			level = mid
		}
		bars[i] = uint16(level * 65535)
	}
	return bars
}

func feed(m *Mapper, n int, bass, mid, treble float64) (omarchy.RGB, int) {
	var rgb omarchy.RGB
	var brightness int
	for i := 0; i < n; i++ {
		rgb, brightness = m.Frame(frame(bass, mid, treble))
	}
	return rgb, brightness
}

func TestSilenceHoldsTheFloorRatherThanGoingDark(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	_, brightness := feed(m, 60, 0, 0, 0)
	if brightness != DefaultConfig().MinBrightness {
		t.Errorf("silence gave brightness %d, want the floor %d",
			brightness, DefaultConfig().MinBrightness)
	}
}

func TestLoudFrameReachesFullBrightnessAlmostAtOnce(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	_, brightness := feed(m, 3, 1, 1, 1)
	if brightness < 90 {
		t.Errorf("three loud frames gave brightness %d, want the attack to be near instant", brightness)
	}
}

// Attack and decay are asymmetric on purpose: the beat arrives instantly and
// fades. A symmetric filter reads as flicker rather than as rhythm.
func TestDecayIsSlowerThanAttack(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	_, loud := feed(m, 5, 1, 1, 1)
	_, afterOneSilentFrame := feed(m, 1, 0, 0, 0)

	dropped := loud - afterOneSilentFrame
	if dropped > loud/2 {
		t.Errorf("one silent frame dropped brightness from %d to %d; decay should be gradual",
			loud, afterOneSilentFrame)
	}
	if afterOneSilentFrame >= loud {
		t.Errorf("brightness did not fall at all: %d then %d", loud, afterOneSilentFrame)
	}
}

// The point of scoring each band against its own baseline: bass-heavy audio,
// which is nearly all audio, must not pin the colour to the accent.
func TestTrebleTransientTakesTheColourFromSteadyBass(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	feed(m, 200, 0.80, 0.10, 0.05)
	if m.Dominant() != Bass {
		t.Fatalf("steady audio should leave the colour where it started, got %s", m.Dominant())
	}

	feed(m, 1, 0.80, 0.10, 0.50)
	if m.Dominant() != Treble {
		t.Errorf("a treble transient ten times its own baseline did not take the colour, got %s",
			m.Dominant())
	}
}

// The same signal at a different volume is the same music: a bass-heavy mix
// must not become "treble dominant" merely by getting louder overall.
func TestUniformGainDoesNotChangeTheDominantBand(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	feed(m, 200, 0.30, 0.10, 0.04)
	before := m.Dominant()
	feed(m, 5, 0.60, 0.20, 0.08)
	if m.Dominant() != before {
		t.Errorf("doubling every band moved the colour from %s to %s", before, m.Dominant())
	}
}

func TestColourEasesInsteadOfJumping(t *testing.T) {
	m := New(DefaultConfig(), testPalette)
	feed(m, 200, 0.80, 0.10, 0.05)

	rgb, _ := feed(m, 1, 0.80, 0.10, 0.50)
	if rgb == testPalette.Treble {
		t.Error("colour jumped straight to the treble colour in one frame")
	}
	if rgb == testPalette.Bass {
		t.Error("colour did not start moving towards the treble colour")
	}

	rgb, _ = feed(m, 40, 0.80, 0.10, 0.50)
	if rgb != testPalette.Treble {
		t.Errorf("colour never arrived: %s, want %s", rgb.Hex(), testPalette.Treble.Hex())
	}
}

// cava's bar count lives in a config file a user can edit, so a frame that is
// not exactly Bands long has to split sensibly rather than panic.
func TestBandEnergiesSplitAnyFrameLength(t *testing.T) {
	for _, n := range []int{1, 2, 3, 8, 9, 16, 32} {
		bars := make([]uint16, n)
		for i := range bars {
			bars[i] = 65535
		}
		e := bandEnergies(bars)
		for i, v := range e {
			if n >= 3 && (v < 0.99 || v > 1.01) {
				t.Errorf("%d bars: band %d energy %.3f, want 1.0", n, i, v)
			}
		}
	}
	if e := bandEnergies(nil); e != [3]float64{} {
		t.Errorf("empty frame gave %v, want zeroes", e)
	}
}

// A frame recorded off the wire during a loud passage — cava's own bars, with
// the sub-50 Hz bin nearly empty as it always is. The ceiling has to be
// reachable on real audio, not only on a synthetic all-bands-full frame.
func TestRecordedLoudFrameReachesTheCeiling(t *testing.T) {
	loud := []uint16{4264, 50141, 55865, 65535, 65535, 65535, 65535, 65535, 41049}

	m := New(DefaultConfig(), testPalette)
	var brightness int
	for i := 0; i < 10; i++ {
		_, brightness = m.Frame(loud)
	}
	if brightness != DefaultConfig().MaxBrightness {
		t.Errorf("a recorded loud frame gave brightness %d, want the ceiling %d",
			brightness, DefaultConfig().MaxBrightness)
	}
}

// Gain corrects a ceiling, so it must not lift the floor: silence stays the
// floor and quiet stays quiet.
func TestGainDoesNotLiftSilenceOrFlattenQuiet(t *testing.T) {
	cfg := DefaultConfig()
	m := New(cfg, testPalette)
	if _, b := feed(m, 60, 0, 0, 0); b != cfg.MinBrightness {
		t.Errorf("silence gave %d, want the floor %d", b, cfg.MinBrightness)
	}

	quiet := New(cfg, testPalette)
	_, quietBrightness := feed(quiet, 60, 0.10, 0.08, 0.05)
	loud := New(cfg, testPalette)
	_, loudBrightness := feed(loud, 60, 0.90, 0.85, 0.80)
	if quietBrightness >= loudBrightness/2 {
		t.Errorf("quiet %d against loud %d: gain flattened the range",
			quietBrightness, loudBrightness)
	}
}
