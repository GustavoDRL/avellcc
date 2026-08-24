package keyboard

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// EffectFunc is a per-frame effect function. frame counts from 0.
//
// It returns what the controller refused. The three effects below used to
// write `_ = ctrl.SetKeyMap(...)`, so a keyboard that rejected every single
// frame was indistinguishable from one lighting up: no error, no exit code, no
// journal line. That is the same failure the `reload` command's errors.Join
// exists to expose, and it was still being discarded here.
type EffectFunc func(ctrl Controller, frame int, opts EffectOpts) error

// EffectOpts holds parameters for software effects.
type EffectOpts struct {
	Speed   int
	R, G, B byte
}

// DefaultEffectOpts returns sensible defaults.
func DefaultEffectOpts() EffectOpts {
	return EffectOpts{Speed: 3, R: 255, G: 255, B: 255}
}

// EffectRunner manages a software LED effect running in a goroutine.
type EffectRunner struct {
	ctrl   Controller
	fps    int
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Diagnostics for the frames the controller refused.
	//
	// A software effect writes fps frames a second — 30 by default — so a
	// device that refuses every write would emit 30 identical lines a second
	// if each one were logged. Only the FIRST is announced, right away, so
	// something reaches the journal while the effect is still running; the
	// rest are counted and reported once, by Err and by Stop.
	mu       sync.Mutex
	firstErr error
	failed   int
	frames   int
	logf     func(format string, v ...any)
}

// NewEffectRunner creates a new runner for the given controller.
func NewEffectRunner(ctrl Controller, fps int) *EffectRunner {
	if fps <= 0 {
		fps = 30
	}
	return &EffectRunner{ctrl: ctrl, fps: fps, logf: log.Printf}
}

// SetLogger replaces where the first refused frame is announced. Only tests
// use it; everything else wants the default, which is the standard logger and
// therefore stderr — the journal, for the systemd units that run this.
func (r *EffectRunner) SetLogger(logf func(format string, v ...any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logf = logf
}

// Start begins running the effect in a goroutine.
func (r *EffectRunner) Start(fn EffectFunc, opts EffectOpts) {
	r.Stop()
	// A new effect starts with a clean tally, so Err never reports the
	// previous effect's refusals against this one.
	r.mu.Lock()
	r.firstErr, r.failed, r.frames = nil, 0, 0
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.runLoop(ctx, fn, opts)
	}()
}

// Stop stops the running effect, waits for cleanup, and returns what the
// controller refused while it ran. Callers that ignore the value still
// compile; the value is there for the ones that do not.
func (r *EffectRunner) Stop() error {
	if r.cancel != nil {
		r.cancel()
		r.wg.Wait()
		r.cancel = nil
	}
	return r.Err()
}

// Err reports the refused frames as one error, and may be called while the
// effect is still running.
func (r *EffectRunner) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstErr == nil {
		return nil
	}
	return fmt.Errorf("%d of %d frames were refused by %s; the first: %w",
		r.failed, r.frames, r.ctrl.Name(), r.firstErr)
}

// recordFrame tallies one frame and announces the first failure.
func (r *EffectRunner) recordFrame(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames++
	if err == nil {
		return
	}
	r.failed++
	if r.firstErr != nil {
		return
	}
	r.firstErr = err
	if r.logf != nil {
		r.logf("avellcc: the keyboard refused a frame of the software effect; "+
			"further failures will only be counted: %v", err)
	}
}

func (r *EffectRunner) runLoop(ctx context.Context, fn EffectFunc, opts EffectOpts) {
	interval := time.Duration(float64(time.Second) / float64(r.fps))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	frame := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The loop keeps going after a refusal on purpose: one dropped
			// frame is no reason to leave the keyboard stuck on the previous
			// one, and a device that is really gone shows up as a failure
			// count equal to the frame count.
			r.recordFrame(fn(r.ctrl, frame, opts))
			frame++
		}
	}
}

// hsvToRGB converts HSV (h in [0,1), s, v in [0,1]) to RGB bytes.
func hsvToRGB(h, s, v float64) (byte, byte, byte) {
	h -= math.Floor(h) // wrap to [0, 1)
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)

	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	case 5:
		r, g, b = v, p, q
	}
	return byte(r * 255), byte(g * 255), byte(b * 255)
}

// RainbowWave is a rainbow wave effect — hue shifts across columns.
func RainbowWave(ctrl Controller, frame int, opts EffectOpts) error {
	rows, cols := ctrl.Rows(), ctrl.Cols()
	colorMap := make(map[[2]int][3]byte, rows*cols)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			hue := float64(col)/float64(cols) + float64(frame)*float64(opts.Speed)*0.005
			r, g, b := hsvToRGB(hue, 1.0, 1.0)
			colorMap[[2]int{row, col}] = [3]byte{r, g, b}
		}
	}
	// One batched update per frame: controllers that push whole rows would
	// otherwise need a separate transfer for every key.
	return ctrl.SetKeyMap(colorMap)
}

// Breathing is a pulsing brightness effect.
func Breathing(ctrl Controller, frame int, opts EffectOpts) error {
	t := float64(frame) * float64(opts.Speed) * 0.02
	factor := (math.Sin(t) + 1.0) / 2.0
	cr := byte(float64(opts.R) * factor)
	cg := byte(float64(opts.G) * factor)
	cb := byte(float64(opts.B) * factor)
	return ctrl.SetAllKeys(cr, cg, cb)
}

// ColorWave is a brightness wave that moves across columns.
func ColorWave(ctrl Controller, frame int, opts EffectOpts) error {
	rows, cols := ctrl.Rows(), ctrl.Cols()
	colorMap := make(map[[2]int][3]byte, rows*cols)
	t := float64(frame) * float64(opts.Speed) * 0.03
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			factor := (math.Sin(t-float64(col)*0.5) + 1.0) / 2.0
			colorMap[[2]int{row, col}] = [3]byte{
				byte(float64(opts.R) * factor),
				byte(float64(opts.G) * factor),
				byte(float64(opts.B) * factor),
			}
		}
	}
	return ctrl.SetKeyMap(colorMap)
}

// SoftwareEffects maps effect names to their functions.
var SoftwareEffects = map[string]EffectFunc{
	"sw_rainbow":   RainbowWave,
	"sw_breathing": Breathing,
	"sw_wave":      ColorWave,
}
