// Command pulsereplay measures the pulse mapping against recorded cava frames.
//
// An audit reported that the band-dominance rule is inverted — that the least
// dynamic band holds the colour almost always, and that the colour flips 4-10
// times a second — using synthetic signals: perfectly steady bands plus a
// periodic kick. Real cava output is neither steady nor noiseless, and the
// debug logs from this machine showed exact palette colours being reached,
// which 4-10 flips a second against a 0.43 s ease would make impossible.
//
// So: record real frames, replay them, and measure. This is what decides
// whether the algorithm changes.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
	"github.com/hugo-andrade/avellcc/internal/pulse"
)

func main() {
	path := flag.String("frames", "", "raw cava capture (9 bars, 16-bit LE, no header)")
	fps := flag.Float64("fps", 30, "frame rate the capture was taken at")
	compare := flag.Bool("compare", false, "compare loudness variants on the same frames")
	active := flag.Float64("active", 0, "only count frames whose loudest bar exceeds this fraction of full scale")
	gain := flag.Float64("gain", 0, "override the gain (0 = the package default)")
	flag.Parse()

	frames, err := readFrames(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(frames) == 0 {
		fmt.Fprintln(os.Stderr, "error: no frames")
		os.Exit(1)
	}

	palette, err := omarchy.CurrentPalette()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg := pulse.DefaultConfig()
	if *gain > 0 {
		cfg.Gain = *gain
	}
	if *compare {
		compareVariants(frames, palette, *fps, *active)
		return
	}
	mapper := pulse.New(cfg, palette)

	seconds := float64(len(frames)) / *fps
	fmt.Printf("%d frames, %.1f s at %.0f fps\n", len(frames), seconds, *fps)
	fmt.Printf("palette: bass %s  mid %s  treble %s\n\n",
		palette.Bass.Hex(), palette.Mid.Hex(), palette.Treble.Hex())

	var occupancy [3]int
	flips := 0
	previous := pulse.Bass
	// "Arrived" means the eased colour is within a just-noticeable distance of
	// the band colour it is heading for — i.e. the bar actually shows a theme
	// colour rather than a blend on the way to one.
	const arrivedWithin = 12.0
	arrived := 0
	var nearest [3]int
	var brightnessSum, brightnessMax float64
	histogram := map[int]int{}

	for i, bars := range frames {
		color, brightness := mapper.Frame(bars)
		band := mapper.Dominant()
		occupancy[band]++
		if i > 0 && band != previous {
			flips++
		}
		previous = band

		d := [3]float64{
			distance(color, palette.Bass),
			distance(color, palette.Mid),
			distance(color, palette.Treble),
		}
		best := 0
		for j := 1; j < 3; j++ {
			if d[j] < d[best] {
				best = j
			}
		}
		nearest[best]++
		if d[best] <= arrivedWithin {
			arrived++
		}

		brightnessSum += float64(brightness)
		brightnessMax = math.Max(brightnessMax, float64(brightness))
		histogram[brightness/10*10]++
	}

	names := [3]string{"bass  ", "mid   ", "treble"}
	colors := [3]string{palette.Bass.Hex(), palette.Mid.Hex(), palette.Treble.Hex()}

	fmt.Println("dominant band — which band holds the colour")
	for i := 0; i < 3; i++ {
		fmt.Printf("  %s %s  %5.1f%%  (%d frames)\n",
			names[i], colors[i], 100*float64(occupancy[i])/float64(len(frames)), occupancy[i])
	}

	fmt.Printf("\ncolour changes: %d in %.1f s = %.2f per second\n", flips, seconds, float64(flips)/seconds)
	fmt.Printf("frames where the eased colour has arrived (within %.0f of a band colour): %.1f%%\n",
		arrivedWithin, 100*float64(arrived)/float64(len(frames)))

	fmt.Println("\nnearest band colour to what the bar actually showed")
	for i := 0; i < 3; i++ {
		fmt.Printf("  %s %s  %5.1f%%\n", names[i], colors[i], 100*float64(nearest[i])/float64(len(frames)))
	}

	fmt.Printf("\nbrightness: mean %.1f, peak %.0f\n", brightnessSum/float64(len(frames)), brightnessMax)
	for bucket := 0; bucket <= 100; bucket += 10 {
		if n := histogram[bucket]; n > 0 {
			fmt.Printf("  %3d-%3d %s %.1f%%\n", bucket, bucket+9,
				bar(float64(n)/float64(len(frames))), 100*float64(n)/float64(len(frames)))
		}
	}
}

func bar(fraction float64) string {
	n := int(fraction * 50)
	out := make([]byte, n)
	for i := range out {
		out[i] = '#'
	}
	return string(out)
}

func distance(a, b omarchy.RGB) float64 {
	dr := float64(a[0]) - float64(b[0])
	dg := float64(a[1]) - float64(b[1])
	db := float64(a[2]) - float64(b[2])
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func readFrames(path string) ([][]uint16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var frames [][]uint16
	buf := make([]byte, pulse.Bands*2)
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return frames, nil
			}
			return nil, err
		}
		bars := make([]uint16, pulse.Bands)
		for i := range bars {
			bars[i] = binary.LittleEndian.Uint16(buf[i*2:])
		}
		frames = append(frames, bars)
	}
}

// compareVariants replays the same frames under different gains so the default
// is chosen from measurement rather than from taste. The columns that matter
// are the median and the share of frames stuck at the floor: a pulse that
// spends three quarters of its time within a few points of the floor is not
// reading as a pulse at all.
// A capture is not all music: a paused player, or a gap between tracks, fills
// it with silence that the mapper correctly renders as a dark bar. Counting
// those frames in the statistics makes a working mapper look broken — the same
// mistake, in the opposite direction, as judging it on synthetic steady tones.
// Every frame is still fed to the mapper so the smoothing state stays honest;
// `active` only decides which frames are counted.
func isActive(bars []uint16, threshold float64) bool {
	if threshold <= 0 {
		return true
	}
	var peak uint16
	for _, v := range bars {
		if v > peak {
			peak = v
		}
	}
	return float64(peak)/65535 > threshold
}

func compareVariants(frames [][]uint16, palette omarchy.Palette, fps float64, active float64) {
	counted := 0
	for _, bars := range frames {
		if isActive(bars, active) {
			counted++
		}
	}
	fmt.Printf("counting %d of %d frames (loudest bar above %.0f%% of scale)\n\n",
		counted, len(frames), active*100)
	fmt.Printf("%-8s %8s %8s %8s %8s %8s %8s\n",
		"gain", "mean", "median", "p95", "peak", "at floor", "flips/s")
	for _, gain := range []float64{1.0, 1.25, 1.5, 2.0, 2.5, 3.0, 4.0} {
		cfg := pulse.DefaultConfig()
		cfg.Gain = gain
		mapper := pulse.New(cfg, palette)

		values := make([]float64, 0, len(frames))
		flips, atFloor := 0, 0
		previous := pulse.Bass
		for i, bars := range frames {
			_, brightness := mapper.Frame(bars)
			if !isActive(bars, active) {
				previous = mapper.Dominant()
				continue
			}
			values = append(values, float64(brightness))
			if brightness <= cfg.MinBrightness+3 {
				atFloor++
			}
			if band := mapper.Dominant(); i > 0 && band != previous {
				flips++
			} else {
				previous = band
			}
			previous = mapper.Dominant()
		}
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)

		var sum float64
		for _, v := range values {
			sum += v
		}
		fmt.Printf("%-8.2f %8.1f %8.0f %8.0f %8.0f %7.1f%% %8.2f\n",
			gain,
			sum/float64(len(values)),
			sorted[len(sorted)/2],
			sorted[int(float64(len(sorted))*0.95)],
			sorted[len(sorted)-1],
			100*float64(atFloor)/float64(len(values)),
			float64(flips)/(float64(len(values))/fps))
	}
}
