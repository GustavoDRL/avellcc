package keyboard

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The EffectRunner drives a software effect at 30 fps and every one of the
// three effects wrote `_ = ctrl.SetKeyMap(...)`. A keyboard that refused every
// frame therefore looked exactly like one lighting up — no error, no exit code,
// nothing in the journal — which is the same silence `avellcc reload` was fixed
// for, on the same hardware.
//
// No device is touched here on purpose: an unopened ITE 8291 has a nil handle
// and refuses every write, which is precisely the failure being tested. The
// temp XDG_CONFIG_HOME keeps the framebuffer mirror away from the real
// ~/.config/avellcc.

// refusingController returns a controller that rejects every write.
func refusingController(t *testing.T) Controller {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := NewITE8291(nil)
	if err := c.SetAllKeys(1, 2, 3); err == nil {
		t.Fatal("an unopened ITE 8291 accepted a write; this fake no longer fakes anything")
	}
	return c
}

// recordingLogger is a logf that a test can read back, guarded because the
// runner writes it from its own goroutine.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) logf(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

func (l *recordingLogger) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// Each of the three effects has to hand its controller error back rather than
// assigning it to _. Table-driven off SoftwareEffects so a fourth effect added
// tomorrow is covered without anyone remembering to add it here.
func TestEverySoftwareEffectReportsWhatTheControllerRefused(t *testing.T) {
	ctrl := refusingController(t)
	if len(SoftwareEffects) == 0 {
		t.Fatal("there are no software effects to check")
	}
	for name, fn := range SoftwareEffects {
		if err := fn(ctrl, 0, DefaultEffectOpts()); err == nil {
			t.Errorf("%s swallowed the controller's refusal", name)
		}
	}
}

// The end of the chain: an effect running against a dead device must leave a
// diagnosis somewhere a person can reach — the log line the unit's journal
// gets, and the aggregate Stop hands back.
func TestRunnerSurfacesRefusedFrames(t *testing.T) {
	ctrl := refusingController(t)

	// 200 fps so a handful of frames land inside a short test; the runner's
	// behaviour does not depend on the rate.
	runner := NewEffectRunner(ctrl, 200)
	log := &recordingLogger{}
	runner.SetLogger(log.logf)

	runner.Start(Breathing, DefaultEffectOpts())
	deadline := time.Now().Add(5 * time.Second)
	for runner.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	// AND THEN LET IT KEEP FAILING. Stopping at the first refusal would run a
	// single frame, and "one frame ran" would satisfy the one-line assertion
	// below no matter what recordFrame did — the de-dup would be untested and
	// the test would stay green with it removed. At 200 fps this is a dozen
	// more refusals, so the one-line check is measuring de-duplication and not
	// the size of the sample.
	time.Sleep(60 * time.Millisecond)
	err := runner.Stop()

	if err == nil {
		t.Fatal("Stop reported success for an effect whose every frame was refused")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the aggregate does not say what happened: %v", err)
	}
	// The count has to be the real one, not a boolean dressed as a number.
	if strings.Contains(err.Error(), "0 of") {
		t.Errorf("the aggregate counted no failures: %v", err)
	}
	// More than one frame really was refused. Without this the sample could be
	// a single frame again — through a slow machine or a future change to the
	// wait above — and the one-line assertion would go back to proving nothing.
	if strings.Contains(err.Error(), "1 of 1 frames") {
		t.Fatalf("only one frame ran, so the de-dup below is untested: %v", err)
	}

	lines := log.all()
	if lines == "" {
		t.Fatal("nothing was logged; a keyboard refusing every frame stays silent")
	}
	// Exactly one line: at 200 fps, logging each failure would bury the journal.
	if len(log.lines) != 1 {
		t.Errorf("expected one announcement, got %d:\n%s", len(log.lines), lines)
	}
}

// A working effect must stay quiet, or the line above becomes noise nobody
// reads — which is the same defect from the other side.
func TestRunnerIsSilentWhenEveryFrameLands(t *testing.T) {
	runner := NewEffectRunner(acceptingController{}, 200)
	log := &recordingLogger{}
	runner.SetLogger(log.logf)

	runner.Start(Breathing, DefaultEffectOpts())
	time.Sleep(60 * time.Millisecond)
	if err := runner.Stop(); err != nil {
		t.Errorf("a controller that accepted everything produced: %v", err)
	}
	if lines := log.all(); lines != "" {
		t.Errorf("a healthy effect logged:\n%s", lines)
	}
}

// Start clears the previous effect's tally, so a failed effect followed by a
// good one does not report the old failures against the new one.
func TestStartClearsThePreviousEffectsTally(t *testing.T) {
	ctrl := refusingController(t)
	runner := NewEffectRunner(ctrl, 200)
	runner.SetLogger(func(string, ...any) {})

	runner.Start(Breathing, DefaultEffectOpts())
	deadline := time.Now().Add(5 * time.Second)
	for runner.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if runner.Err() == nil {
		t.Fatal("the refusing run never recorded a failure")
	}

	runner.Start(Breathing, DefaultEffectOpts())
	if err := runner.Err(); err != nil {
		t.Errorf("a fresh Start still reports the previous run: %v", err)
	}
	_ = runner.Stop()
}

// acceptingController is a controller whose writes all succeed. It exists only
// so the silence test above has something healthy to drive; nothing here talks
// to a device.
type acceptingController struct{}

func (acceptingController) Name() string                                 { return "fake" }
func (acceptingController) Open() error                                  { return nil }
func (acceptingController) Close() error                                 { return nil }
func (acceptingController) Rows() int                                    { return 6 }
func (acceptingController) Cols() int                                    { return 21 }
func (acceptingController) SetBrightness(int) error                      { return nil }
func (acceptingController) SetKeyColor(int, int, byte, byte, byte) error { return nil }
func (acceptingController) SetAllKeys(byte, byte, byte) error            { return nil }
func (acceptingController) SetKeyMap(map[[2]int][3]byte) error           { return nil }
func (acceptingController) SetHWAnimation(int, int) error                { return nil }
func (acceptingController) HWEffects() map[string]int                    { return nil }
func (acceptingController) KeymapID() string                             { return "fake" }
func (acceptingController) DefaultKeymap() map[string][2]int             { return nil }
func (acceptingController) Off() error                                   { return nil }
func (acceptingController) GetFirmwareInfo() ([]byte, error)             { return nil, nil }
