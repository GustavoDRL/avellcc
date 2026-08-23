// Package omarchy reads the colours of the currently applied Omarchy theme.
//
// The theme-set hooks already pick a lightbar colour with an awk script. That
// works for a one-shot write, but the pulse daemon needs the same choice in
// memory, re-made whenever the theme changes, so the rule lives here and the
// hook calls into it rather than the two drifting apart.
package omarchy

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RGB is a colour as the lightbar takes it.
type RGB [3]byte

// Hex renders the colour as #rrggbb.
func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]) }

// Palette is the three-colour set the pulse maps frequency bands onto. Bass
// takes the theme's own accent — the colour that reads as its identity — and
// the other two are chosen to stay visibly apart from it and from each other.
type Palette struct {
	Bass   RGB // the theme's accent
	Mid    RGB // the palette colour farthest around the hue circle from the accent
	Treble RGB // the colour farthest from both of the above
}

// CandidateKeys are the palette entries every stock theme defines. `orange`
// and `brown` are missing from three of them, so they are not candidates.
var CandidateKeys = []string{
	"red", "yellow", "green", "cyan", "blue", "magenta",
	"bright_red", "bright_yellow", "bright_green", "bright_cyan", "bright_blue", "bright_magenta",
}

// A candidate has to survive both floors to be usable on the bar. Saturation
// alone is not enough: it admits near-blacks, whose hue is real but whose
// output is indistinguishable from the bar being off. The awk picker in the
// hook only filtered saturation, and at 0.15 it let `lumon` pick a near-white
// and `solitude` a near-black. These floors are the deliberate difference.
const (
	minSaturation = 0.20
	minValue      = 0.35
)

// ThemeDir is where Omarchy publishes the theme currently applied.
func ThemeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
}

// ColorsPath is the current theme's colours file.
func ColorsPath() string { return filepath.Join(ThemeDir(), "colors.toml") }

// ThemeNamePath holds the display name of the theme currently applied.
func ThemeNamePath() string {
	return filepath.Join(filepath.Dir(ThemeDir()), "theme.name")
}

var colorLine = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"?#?([0-9A-Fa-f]{6})"?`)

// ReadColors parses the flat key = "#rrggbb" pairs out of a colors.toml.
// Anything that is not a six-digit colour is ignored: the file also carries
// `mode = "dark"` and similar, and a theme is free to add keys.
func ReadColors(path string) (map[string]RGB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	colors := make(map[string]RGB)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := colorLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[2], 16, 32)
		if err != nil {
			continue
		}
		colors[strings.ToLower(m[1])] = RGB{byte(v >> 16), byte(v >> 8), byte(v)}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if _, ok := colors["accent"]; !ok {
		return nil, fmt.Errorf("%s defines no accent colour", path)
	}
	return colors, nil
}

// CurrentPalette reads the applied theme and derives the pulse palette.
func CurrentPalette() (Palette, error) {
	colors, err := ReadColors(ColorsPath())
	if err != nil {
		return Palette{}, err
	}
	// An override stands in for the theme's accent while it is in force; see
	// override.go. Mid and treble are still derived, so they move with it.
	if c, ok := AccentOverride(); ok {
		colors["accent"] = c
	}
	return PaletteFrom(colors), nil
}

// PaletteFrom derives the three pulse colours from a theme's colour map.
//
// Mid repeats the rule the theme-set hook already uses: of the theme's own
// palette colours, the one whose hue sits farthest from the accent, with
// saturation breaking ties. Treble then takes whichever candidate stays
// farthest from *both*, so the three bands never collapse into neighbouring
// hues on a theme whose palette is lopsided.
//
// A monochrome theme — vantablack, white — has no hue to be far from and no
// saturated colour to fall back on. All three bands then take the accent,
// because the theme itself has one colour.
func PaletteFrom(colors map[string]RGB) Palette {
	accent := colors["accent"]
	p := Palette{Bass: accent, Mid: accent, Treble: accent}

	type candidate struct {
		key string
		rgb RGB
		hue float64 // -1 when achromatic
		sat float64
	}
	var candidates []candidate
	for _, key := range CandidateKeys {
		rgb, ok := colors[key]
		if !ok {
			continue
		}
		h, s := hueSat(rgb)
		if s < minSaturation || value(rgb) < minValue {
			continue
		}
		candidates = append(candidates, candidate{key, rgb, h, s})
	}
	// A map has no order and the scoring can tie exactly, most often between a
	// colour and its identical bright_ variant. Sorting keeps the answer the
	// same from run to run.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].key < candidates[j].key })
	if len(candidates) == 0 {
		return p
	}

	accentHue, _ := hueSat(accent)

	// An achromatic accent gives nothing to be far from, so the most saturated
	// candidate is the best available answer.
	best := -1.0
	for _, c := range candidates {
		score := c.sat
		if accentHue >= 0 {
			score = hueDistance(accentHue, c.hue) + c.sat
		}
		if score > best {
			best, p.Mid = score, c.rgb
		}
	}

	midHue, _ := hueSat(p.Mid)
	best = -1.0
	for _, c := range candidates {
		// The colour already taken by mid cannot also be treble. Without this
		// a theme with a single usable candidate — solitude has exactly one,
		// every other entry being grey or too dark — handed the same colour to
		// two of the three bands, and the bar could then never distinguish
		// them.
		if c.rgb == p.Mid {
			continue
		}
		// Distance to the nearer of the two colours already chosen is what
		// decides: a candidate is only useful if it is far from both.
		score := hueDistance(midHue, c.hue)
		if accentHue >= 0 {
			score = math.Min(score, hueDistance(accentHue, c.hue))
		}
		score += c.sat // saturation only breaks ties
		if score > best {
			best, p.Treble = score, c.rgb
		}
	}
	// Nothing else survived the floors. Falling back to the accent says "this
	// theme has two colours, not three", which is true, and keeps treble
	// distinguishable from mid.
	if best < 0 {
		p.Treble = accent
	}
	return p
}

// value is the V of HSV: how much light the colour actually puts out.
func value(c RGB) float64 {
	return math.Max(float64(c[0]), math.Max(float64(c[1]), float64(c[2]))) / 255
}

// hueSat returns the colour's hue in degrees and its saturation. Hue is -1 for
// an achromatic colour, which has none.
func hueSat(c RGB) (float64, float64) {
	r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	d := mx - mn

	sat := 0.0
	if mx > 0 {
		sat = d / mx
	}
	if d == 0 {
		return -1, sat
	}

	var h float64
	switch mx {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * (((b - r) / d) + 2)
	default:
		h = 60 * (((r - g) / d) + 4)
	}
	if h < 0 {
		h += 360
	}
	return h, sat
}

// hueDistance is the shorter way around the hue circle, in degrees. An
// achromatic colour is treated as maximally distant: it clashes with nothing.
func hueDistance(a, b float64) float64 {
	if a < 0 || b < 0 {
		return 180
	}
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}
