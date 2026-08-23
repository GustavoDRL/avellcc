// Command pulserate measures how much sustained HID write pressure the ITE
// 8233 chassis lightbar tolerates.
//
// The theme hooks write two packets per theme change; an audio-reactive pulse
// would write two packets per frame, continuously. Nothing here has ever been
// exercised at that rate, and this controller answers a malformed or unwelcome
// packet with success, so the only honest test is a visual one: ramp the rate,
// keep the bar visibly moving, and watch it.
//
// Every packet uses the verified variant bytes and never leaves static mode.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hugo-andrade/avellcc/internal/hidraw"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

func main() {
	rates := flag.String("rates", "5,10,20,30,60", "frame rates to try, in Hz")
	seconds := flag.Float64("seconds", 3, "seconds to hold each rate")
	flag.Parse()

	path, product, err := lightbar.FindITE8233()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	ctrl := lightbar.NewITE8233(&hidraw.HidrawDevice{Path: path}, product)
	if err := ctrl.Open(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer func() { _ = ctrl.Close() }()

	fmt.Printf("device %s (%04x:%04x)\n\n", path, lightbar.VID8233, product)
	fmt.Printf("%-8s %-10s %-10s %-12s %s\n", "target", "achieved", "frames", "write err", "worst write")

	for _, field := range strings.Split(*rates, ",") {
		hz, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || hz <= 0 {
			continue
		}
		achieved, frames, failures, worst := run(ctrl, hz, *seconds)
		fmt.Printf("%-8.0f %-10.1f %-10d %-12d %s\n", hz, achieved, frames, failures, worst)
	}

	// Leave the bar where the theme wants it rather than mid-sweep.
	_ = ctrl.SetColor(0xF9, 0xE2, 0xAF, 80)
	fmt.Println("\nbar restored to the theme colour")
}

// run drives the bar at hz for the given duration, sweeping hue and brightness
// so a stall, a skip, or a dead bar is visible rather than inferred.
func run(ctrl *lightbar.ITE8233, hz, seconds float64) (achieved float64, frames, failures int, worst time.Duration) {
	period := time.Duration(float64(time.Second) / hz)
	deadline := time.Now().Add(time.Duration(seconds * float64(time.Second)))
	start := time.Now()

	for now := time.Now(); now.Before(deadline); now = time.Now() {
		t := now.Sub(start).Seconds()
		r, g, b := hueRGB(math.Mod(t*hz*8, 360))
		brightness := 30 + int(60*(0.5+0.5*math.Sin(2*math.Pi*t*2)))

		writeStart := time.Now()
		err := ctrl.SetColor(r, g, b, brightness)
		if elapsed := time.Since(writeStart); elapsed > worst {
			worst = elapsed
		}
		if err != nil {
			failures++
		}
		frames++

		if sleep := period - time.Since(now); sleep > 0 {
			time.Sleep(sleep)
		}
	}
	return float64(frames) / time.Since(start).Seconds(), frames, failures, worst
}

func hueRGB(h float64) (byte, byte, byte) {
	c := 1.0
	x := 1 - math.Abs(math.Mod(h/60, 2)-1)
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return byte(r * 255), byte(g * 255), byte(b * 255)
}
