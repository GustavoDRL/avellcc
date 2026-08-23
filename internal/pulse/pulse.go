// Package pulse turns a spectrum frame into a lightbar colour and brightness.
//
// It is deliberately free of both the audio source and the HID transport: the
// daemon feeds it cava's bars and writes whatever comes back, and everything
// here is a pure function of the frames seen so far, which is what makes the
// mapping testable without hardware or sound.
package pulse

import (
	"math"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// Bands is the number of spectrum bars the mapper expects, split evenly into
// bass, mid and treble. Nine keeps that split exact.
const Bands = 9

// Config tunes the mapping. The zero value is not useful; use DefaultConfig.
type Config struct {
	// Brightness floor and ceiling, on the controller's 0-100 scale. The floor
	// is not zero: a bar that goes fully dark between beats reads as broken
	// rather than as rhythm.
	MinBrightness int
	MaxBrightness int

	// Attack and Decay are the per-frame weights of a new level reading when
	// it is louder and quieter than the current one. Attack is deliberately
	// close to 1 and Decay far from it: a beat has to arrive instantly and
	// fade out, which is what reads as a pulse rather than as a flicker.
	Attack float64
	Decay  float64

	// ColorEase is the per-frame weight of the target colour. Switching hue
	// the instant the dominant band changes strobes badly at 30 fps.
	ColorEase float64

	// BaselineEase is the per-frame weight of a band's own running average.
	// Small, so the average spans seconds rather than beats.
	BaselineEase float64

	// Dominance is how far above its own baseline a band must sit before it
	// can take the colour from the band that currently holds it. Without it,
	// noise around the baseline makes the colour jitter between bands.
	Dominance float64

	// Gain rescales loudness before it becomes brightness.
	//
	// It is not a volume knob, it corrects an off-by-construction: cava's
	// autosens normalises so that the *loudest bar* reaches full scale, never
	// the average of them. Loudness here is a mean across three bands, so it
	// can only reach 1.0 when every band peaks in the same frame, which music
	// does not do.
	//
	// The value comes from measurement, not taste. Replaying 90 s of recorded
	// cava output through this mapper (tools/pulsereplay), counting only the
	// frames that actually carry music, the ceiling reached: 64 at gain 1.25,
	// 96 at 2.0, and 100 with the top 5% clipped at 2.5. 2.0 uses the range
	// without flattening the loud end.
	Gain float64
}

// DefaultConfig is tuned for 30 fps.
func DefaultConfig() Config {
	return Config{
		MinBrightness: 12,
		MaxBrightness: 100,
		Attack:        0.85,
		Decay:         0.12,
		ColorEase:     0.30,
		BaselineEase:  0.02,
		Dominance:     1.15,
		Gain:          2.0,
	}
}

// Band identifies which third of the spectrum is currently driving the colour.
type Band int

const (
	Bass Band = iota
	Mid
	Treble
)

func (b Band) String() string {
	switch b {
	case Mid:
		return "mid"
	case Treble:
		return "treble"
	default:
		return "bass"
	}
}

// Mapper holds the smoothing state between frames.
type Mapper struct {
	cfg     Config
	palette omarchy.Palette

	level     float64
	baseline  [3]float64
	color     [3]float64
	dominant  Band
	haveColor bool
}

// New creates a mapper for a palette.
func New(cfg Config, palette omarchy.Palette) *Mapper {
	return &Mapper{cfg: cfg, palette: palette}
}

// SetConfig swaps the tuning without disturbing the smoothing state, so a
// setting changed mid-track takes effect on the next frame rather than on the
// next track.
func (m *Mapper) SetConfig(cfg Config) { m.cfg = cfg }

// Config returns the tuning currently in use.
func (m *Mapper) Config() Config { return m.cfg }

// SetPalette swaps the colours without disturbing the smoothing state, so a
// theme change mid-track eases into the new colours instead of jumping.
func (m *Mapper) SetPalette(p omarchy.Palette) { m.palette = p }

// Palette returns the colours currently in use.
func (m *Mapper) Palette() omarchy.Palette { return m.palette }

// Dominant reports which band last took the colour.
func (m *Mapper) Dominant() Band { return m.dominant }

// Frame maps one spectrum frame to what the bar should show. bars carries
// Bands values in cava's raw 16-bit range, lowest frequency first.
func (m *Mapper) Frame(bars []uint16) (omarchy.RGB, int) {
	energies := bandEnergies(bars)

	// Music sits with its energy in the bass almost all the time, so the loudest
	// band in absolute terms is the bass in nearly every frame and picking that
	// way would pin the colour to the accent forever. What carries the rhythm is
	// a band rising above its *own* recent average, so that is what is compared.
	var scores [3]float64
	for i, e := range energies {
		if m.baseline[i] == 0 {
			m.baseline[i] = e
		}
		scores[i] = e / (m.baseline[i] + 1e-6)
		m.baseline[i] += (e - m.baseline[i]) * m.cfg.BaselineEase
	}

	// The band holding the colour keeps it until another beats it by the
	// dominance margin, which is what stops two near-equal bands from trading
	// the colour back and forth every frame.
	challenger := Bass
	for i := 1; i < 3; i++ {
		if scores[i] > scores[challenger] {
			challenger = Band(i)
		}
	}
	if scores[challenger] > scores[m.dominant]*m.cfg.Dominance {
		m.dominant = challenger
	}

	target := m.bandColor(m.dominant)
	if !m.haveColor {
		m.color = [3]float64{float64(target[0]), float64(target[1]), float64(target[2])}
		m.haveColor = true
	} else {
		for i := range m.color {
			m.color[i] += (float64(target[i]) - m.color[i]) * m.cfg.ColorEase
		}
	}

	loudness := clamp((energies[0]+energies[1]+energies[2])/3*m.cfg.Gain, 0, 1)
	ease := m.cfg.Decay
	if loudness > m.level {
		ease = m.cfg.Attack
	}
	m.level += (loudness - m.level) * ease

	rgb := omarchy.RGB{
		byte(math.Round(clamp(m.color[0], 0, 255))),
		byte(math.Round(clamp(m.color[1], 0, 255))),
		byte(math.Round(clamp(m.color[2], 0, 255))),
	}
	span := float64(m.cfg.MaxBrightness - m.cfg.MinBrightness)
	brightness := float64(m.cfg.MinBrightness) + span*clamp(m.level, 0, 1)
	return rgb, int(math.Round(brightness))
}

func (m *Mapper) bandColor(b Band) omarchy.RGB {
	switch b {
	case Mid:
		return m.palette.Mid
	case Treble:
		return m.palette.Treble
	default:
		return m.palette.Bass
	}
}

// bandEnergies collapses the bars into three normalised 0-1 band energies.
// Short frames are tolerated rather than rejected: cava's bar count is set in
// a config file that a user can edit.
func bandEnergies(bars []uint16) [3]float64 {
	var sums [3]float64
	var counts [3]int
	if len(bars) == 0 {
		return sums
	}
	for i, v := range bars {
		band := i * 3 / len(bars)
		if band > 2 {
			band = 2
		}
		sums[band] += float64(v) / 65535
		counts[band]++
	}
	for i := range sums {
		if counts[i] > 0 {
			sums[i] /= float64(counts[i])
		}
	}
	return sums
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
