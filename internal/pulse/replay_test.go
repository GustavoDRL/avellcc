package pulse

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"sort"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// testdata/frames.bin is 50 seconds of real cava output, recorded from this
// machine's PipeWire capture while music played, in exactly the format the
// daemon reads: 9 bars, 16-bit little-endian, no header.
//
// It exists because every other test in this package drives the mapper with
// synthetic signals, and an audit's two most severe claims about this
// algorithm — that the least dynamic band holds the colour ~93% of the time,
// and that the colour flips 4-10 times a second into a mush — were derived
// from synthetic signals and do not reproduce on real input. Steady tones plus
// a periodic kick are not what cava emits: autosens and its smoothing make the
// real thing noisier and slower. Whatever this algorithm is changed to next has
// to be judged against this file, not against a tone generator.
func loadFrames(t *testing.T) [][]uint16 {
	t.Helper()
	f, err := os.Open("testdata/frames.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var frames [][]uint16
	buf := make([]byte, Bands*2)
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return frames
			}
			t.Fatal(err)
		}
		bars := make([]uint16, Bands)
		for i := range bars {
			bars[i] = binary.LittleEndian.Uint16(buf[i*2:])
		}
		frames = append(frames, bars)
	}
}

var replayPalette = omarchy.Palette{
	Bass:   omarchy.RGB{0x10, 0xab, 0xa2},
	Mid:    omarchy.RGB{0xff, 0x48, 0x48},
	Treble: omarchy.RGB{0x8f, 0xa0, 0xc4},
}

// A frame carries music when its loudest bar clears a tenth of full scale.
// Silence is correctly rendered as a dark bar, so counting it would make a
// working mapper look broken.
func active(bars []uint16) bool {
	var peak uint16
	for _, v := range bars {
		if v > peak {
			peak = v
		}
	}
	return float64(peak)/65535 > 0.10
}

// No band may be starved. The whole point of scoring each band against its own
// baseline is that the colour moves; a mapper that parks on one band would
// pass every other test in this package.
func TestOnRealAudioEveryBandGetsTheColour(t *testing.T) {
	frames := loadFrames(t)
	m := New(DefaultConfig(), replayPalette)

	var occupancy [3]int
	counted := 0
	for _, bars := range frames {
		m.Frame(bars)
		if !active(bars) {
			continue
		}
		occupancy[m.Dominant()]++
		counted++
	}
	if counted < 200 {
		t.Fatalf("only %d frames carry music; the fixture is not usable", counted)
	}
	for band, n := range occupancy {
		share := 100 * float64(n) / float64(counted)
		t.Logf("%-6s %5.1f%%", Band(band), share)
		if share < 5 {
			t.Errorf("%s holds the colour %.1f%% of the time — a band is being starved",
				Band(band), share)
		}
	}
}

// Flipping faster than the colour ease can follow would leave the bar showing
// a blend that is never any of the theme's colours. Measured at 1.8/s on this
// recording.
//
// Honest about its reach: removing the dominance margin entirely does not push
// the rate past this bound on real audio, so this test does not prove the
// margin earns its keep — it is a guard against a gross regression into
// strobing or into parking, nothing finer.
func TestOnRealAudioTheColourChangesAtAWatchablePace(t *testing.T) {
	frames := loadFrames(t)
	m := New(DefaultConfig(), replayPalette)

	flips, counted := 0, 0
	previous := Bass
	for i, bars := range frames {
		m.Frame(bars)
		if !active(bars) {
			previous = m.Dominant()
			continue
		}
		if i > 0 && m.Dominant() != previous {
			flips++
		}
		previous = m.Dominant()
		counted++
	}
	perSecond := float64(flips) / (float64(counted) / 30)
	t.Logf("%.2f colour changes per second over %d frames", perSecond, counted)
	if perSecond > 4 {
		t.Errorf("%.2f changes per second: faster than the 0.43 s colour ease can follow, "+
			"so the bar would show a blend rather than the theme's colours", perSecond)
	}
	if perSecond < 0.2 {
		t.Errorf("%.2f changes per second: the colour is effectively parked", perSecond)
	}
}

// The reason the default gain is 2.0 and not 1.25. On real music the mapper
// must use the brightness range, not hug the floor.
func TestOnRealAudioBrightnessUsesItsRange(t *testing.T) {
	frames := loadFrames(t)
	cfg := DefaultConfig()
	m := New(cfg, replayPalette)

	var values []float64
	for _, bars := range frames {
		_, brightness := m.Frame(bars)
		if active(bars) {
			values = append(values, float64(brightness))
		}
	}
	sort.Float64s(values)
	median := values[len(values)/2]
	p95 := values[int(float64(len(values))*0.95)]
	peak := values[len(values)-1]
	t.Logf("median %.0f  p95 %.0f  peak %.0f", median, p95, peak)

	if peak < 80 {
		t.Errorf("peak brightness %.0f: the loudest passage of real music should get near the ceiling", peak)
	}
	if median < 20 {
		t.Errorf("median brightness %.0f: the bar is hugging its floor while music plays", median)
	}
	// Clipping is the other failure: a gain so high that the top of the range
	// is flat carries no dynamics.
	if p95 >= float64(cfg.MaxBrightness) && median > 70 {
		t.Errorf("median %.0f with p95 at the ceiling: the loud end is clipped flat", median)
	}
}

// Whatever the smoothing does, the output has to stay a colour and a legal
// brightness on real input.
func TestOnRealAudioOutputStaysInRange(t *testing.T) {
	frames := loadFrames(t)
	cfg := DefaultConfig()
	m := New(cfg, replayPalette)

	for i, bars := range frames {
		color, brightness := m.Frame(bars)
		if brightness < cfg.MinBrightness || brightness > cfg.MaxBrightness {
			t.Fatalf("frame %d: brightness %d outside %d-%d", i, brightness, cfg.MinBrightness, cfg.MaxBrightness)
		}
		for c, v := range color {
			if math.IsNaN(float64(v)) {
				t.Fatalf("frame %d: channel %d is not a number", i, c)
			}
		}
	}
}
