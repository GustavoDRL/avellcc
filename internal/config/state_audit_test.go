package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The state file got the same audit the settings file got, and it failed the
// same way: it was written with os.WriteFile, which truncates before it writes.
// These are regressions for what that cost, reproduced before it was fixed.

// withStateDir points ConfigDir at an empty temp directory.
func withStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "avellcc")
}

// The port of TestReadersNeverSeeAPartialFile to the state file. A reader
// racing a writer used to get an empty bundle — 46922 of 80000 reads did —
// and an empty bundle is not an error anywhere: the lightbar resolves it to
// white at MaxBrightness8233, and reload prints "No saved state found".
func TestStateReadersNeverSeeAnEmptyBundle(t *testing.T) {
	withStateDir(t)

	if err := SaveStateBundle(map[string]any{
		"keyboard": map[string]any{"mode": "static", "brightness": float64(8)},
		"lightbar": map[string]any{"mode": "active", "brightness": float64(40)},
	}); err != nil {
		t.Fatal(err)
	}

	const readsPerReader = 20000
	const readers = 4

	done := make(chan struct{})
	var writes atomic.Int64
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			brightness := float64(3 + i%7)
			if err := SaveStateBundle(map[string]any{
				"keyboard": map[string]any{"mode": "static", "brightness": brightness},
				"lightbar": map[string]any{"mode": "active", "brightness": brightness},
			}); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			writes.Add(1)
		}
	}()

	var empty, halved atomic.Int64
	var pool sync.WaitGroup
	for r := 0; r < readers; r++ {
		pool.Add(1)
		go func() {
			defer pool.Done()
			for i := 0; i < readsPerReader; i++ {
				bundle := LoadStateBundle()
				if len(bundle) == 0 {
					empty.Add(1)
					continue
				}
				kb, ok := bundle["keyboard"].(map[string]any)
				if !ok || len(kb) == 0 {
					halved.Add(1)
					continue
				}
				if _, ok := GetInt(kb, "brightness"); !ok {
					halved.Add(1)
				}
			}
		}()
	}
	pool.Wait()
	close(done)
	writer.Wait()

	if n := empty.Load(); n > 0 {
		t.Errorf("%d of %d reads got an empty bundle", n, readers*readsPerReader)
	}
	if n := halved.Load(); n > 0 {
		t.Errorf("%d of %d reads got a bundle missing the keyboard half", n, readers*readsPerReader)
	}
	// A run where the writer barely ran proves nothing, the same way the
	// settings version skips when too few reads land.
	if n := writes.Load(); n < 20 {
		t.Skipf("only %d writes landed against %d reads; inconclusive", n, readers*readsPerReader)
	}
}

// An atomic write stops a reader seeing half a file. It does nothing about two
// writers that both load the same bundle and each save only its own half — the
// state file has four writers with no coordination, so the load-modify-save
// itself has to be locked.
func TestConcurrentStateUpdatesNeverLoseAKey(t *testing.T) {
	withStateDir(t)

	keys := []string{"alpha", "bravo", "charlie", "delta", "echo",
		"foxtrot", "golf", "hotel", "india", "juliett"}

	var wg sync.WaitGroup
	errs := make(chan error, len(keys))
	for i, key := range keys {
		wg.Add(1)
		go func(key string, value int) {
			defer wg.Done()
			err := UpdateStateBundle(func(bundle map[string]any) error {
				state, _ := bundle["keyboard"].(map[string]any)
				if state == nil {
					state = map[string]any{}
				}
				// A real writer talks to the device between the load and the
				// save; without that pause the window is too narrow to show
				// the defect on every run, and the defect is still there.
				time.Sleep(time.Millisecond)
				state[key] = float64(value)
				bundle["keyboard"] = state
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(key, i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent update failed: %v", err)
	}

	bundle := LoadStateBundle()
	state, _ := bundle["keyboard"].(map[string]any)
	for i, key := range keys {
		got, ok := GetInt(state, key)
		if !ok {
			t.Errorf("%s was lost by a concurrent update", key)
			continue
		}
		if got != i {
			t.Errorf("%s = %d, want %d", key, got, i)
		}
	}
}

// A state file that does not parse is not the same thing as no state file:
// one is corruption, the other is a first run. Neither may crash, and the
// keyboard half of a good file must survive a reload.
func TestLoadStateBundleSeparatesMissingFromUnreadable(t *testing.T) {
	dir := withStateDir(t)

	if got := LoadStateBundle(); len(got) != 0 {
		t.Errorf("a missing state file gave %v, want an empty bundle", got)
	}

	if err := SaveStateBundle(map[string]any{
		"keyboard": map[string]any{"brightness": float64(6)},
	}); err != nil {
		t.Fatal(err)
	}
	state, _ := LoadStateBundle()["keyboard"].(map[string]any)
	if b, ok := GetInt(state, "brightness"); !ok || b != 6 {
		t.Errorf("brightness came back as %v (ok=%v), want 6", b, ok)
	}

	if err := writeRawState(dir, "{\"keyboard\": {\"brig"); err != nil {
		t.Fatal(err)
	}
	if got := LoadStateBundle(); len(got) != 0 {
		t.Errorf("a truncated state file gave %v, want an empty bundle", got)
	}
}

// writeRawState drops a body into the state file without going through the
// writer, which is the only way to produce the corruption the writer prevents.
func writeRawState(dir, body string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o644)
}
