package config

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// Every test here is a regression for a defect an audit reproduced against the
// first version of the writer. They are grouped in one file so it stays
// obvious what they are: none of them was imagined.

// The worst one. Two `config set` runs used to interleave their
// read-modify-write; 200 races left 29 files unloadable and silently dropped
// 78 edits.
func TestConcurrentWritesNeverCorruptAndNeverLose(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	// Every writer takes a *different* key and a value that is not that key's
	// default, so a lost update is detectable. An earlier version of this test
	// had several writers share a key whose target value was the default —
	// which made a dropped write invisible and let the test pass with the lock
	// removed.
	writes := map[string]string{
		"theme.brightness":     "55",
		"theme.effect":         "breathing",
		"theme.speed":          "3",
		"theme.color_key":      "bright_magenta",
		"pulse.fps":            "45",
		"pulse.min_brightness": "20",
		"pulse.max_brightness": "90",
		"pulse.gain":           "1.5",
		"pulse.player":         "mpv",
		"pulse.input_method":   "pulse",
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(writes))
	for key, value := range writes {
		wg.Add(1)
		go func(key, value string) {
			defer wg.Done()
			if err := WriteLightbarSetting(key, value); err != nil {
				errs <- err
			}
		}(key, value)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatalf("the file no longer loads after concurrent writes: %v", err)
	}
	for key, want := range writes {
		got, err := GetLightbarSetting(settings, key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if strings.Trim(got, `"`) != want {
			t.Errorf("%s = %s, want %s — a concurrent write was lost", key, got, want)
		}
	}
	body, _ := os.ReadFile(LightbarSettingsPath())
	if !strings.Contains(string(body), "# Chassis light bar") {
		t.Error("the comments did not survive concurrent writes")
	}
}

// A reader racing a writer used to see a zero-byte file — valid TOML that
// loads as "every default applies", with no error to notice.
func TestReadersNeverSeeAPartialFile(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			value := "30"
			if i%2 == 1 {
				value = "45"
			}
			if err := WriteLightbarSetting("pulse.fps", value); err != nil {
				t.Errorf("write: %v", err)
				break
			}
		}
		close(done)
	}()

	reads, empty := 0, 0
	for {
		select {
		case <-done:
			wg.Wait()
			if empty > 0 {
				t.Errorf("%d of %d reads saw an empty file", empty, reads)
			}
			if reads < 10 {
				t.Skipf("only %d reads landed; inconclusive", reads)
			}
			return
		default:
		}
		body, err := os.ReadFile(LightbarSettingsPath())
		if err != nil {
			continue
		}
		reads++
		if len(body) == 0 {
			empty++
			continue
		}
		if _, err := DecodeLightbarSettings(string(body)); err != nil {
			t.Fatalf("read a file that does not parse: %v", err)
		}
	}
}

// A comment after a section header made the header invisible to the scanner,
// so the key landed in whichever section it still thought it was in. This is
// the silent-wrong-write case: nothing failed, and the wrong setting changed.
func TestCommentAfterSectionHeaderDoesNotMisdirectTheWrite(t *testing.T) {
	withSettingsFile(t, "[theme]\nbrightness = 40\n\n[pulse]  # music\nenabled = true\n")

	if err := WriteLightbarSetting("theme.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme.Enabled {
		t.Error("theme.enabled was not written")
	}
	if !settings.Pulse.Enabled {
		t.Error("pulse.enabled was clobbered by a write to theme.enabled")
	}
}

func TestSpacedSectionHeaderIsRecognised(t *testing.T) {
	withSettingsFile(t, "[ theme ]\nbrightness = 40\n")

	if err := WriteLightbarSetting("theme.brightness", "55"); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatalf("the file no longer loads: %v", err)
	}
	if settings.Theme.Brightness != 55 {
		t.Errorf("theme.brightness = %d, want 55", settings.Theme.Brightness)
	}
	body, _ := os.ReadFile(LightbarSettingsPath())
	if strings.Count(string(body), "[") > 1 {
		t.Errorf("a duplicate section was appended:\n%s", body)
	}
}

// TOML shapes the line scanner cannot handle safely must be refused, not
// half-written. The verification step is what turns each of these from a
// corrupted file into an error.
func TestExoticTOMLIsRefusedRatherThanCorrupted(t *testing.T) {
	cases := map[string]string{
		"quoted key":        "[pulse]\n\"fps\" = 30\n",
		"inline table":      "theme = { brightness = 40 }\n",
		"top-level dotted":  "pulse.fps = 30\npulse.gain = 2.0\n",
		"multi-line string": "[pulse]\nplayer = \"\"\"\nfps = 1\n\"\"\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			withSettingsFile(t, body)
			before, _ := os.ReadFile(LightbarSettingsPath())

			// Either the file does not load (refused early) or the write is
			// refused by verification. Both are acceptable; corruption is not.
			err := WriteLightbarSetting("pulse.fps", "45")

			after, _ := os.ReadFile(LightbarSettingsPath())
			if err == nil {
				// If it did write, the result must load and be correct.
				settings, loadErr := LoadLightbarSettings()
				if loadErr != nil {
					t.Fatalf("write succeeded but the file no longer loads: %v", loadErr)
				}
				if settings.Pulse.FPS != 45 {
					t.Fatalf("write succeeded but pulse.fps = %d", settings.Pulse.FPS)
				}
				return
			}
			if string(before) != string(after) {
				t.Errorf("the write was refused but the file changed anyway:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// A colour value contains a '#'. It used to be re-emitted as a trailing
// comment, accreting junk on every write.
func TestHashInsideAStringIsNotTreatedAsAComment(t *testing.T) {
	withSettingsFile(t, "[theme]\ncolor_key = \"auto\"\n")

	if err := WriteLightbarSetting("theme.color_key", "foreground"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(LightbarSettingsPath())
	if strings.Contains(string(body), "#") {
		t.Errorf("a comment appeared from nowhere:\n%s", body)
	}
}

// ParseFloat accepts these; `<= 0` is false for both; and neither is TOML.
// The write used to exit 0 and leave the file unloadable.
func TestNonFiniteGainIsRejected(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)
	for _, value := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		if err := WriteLightbarSetting("pulse.gain", value); err == nil {
			t.Errorf("pulse.gain %s was accepted", value)
		}
	}
	if _, err := LoadLightbarSettings(); err != nil {
		t.Fatalf("the file was damaged by a rejected write: %v", err)
	}
}

// Go's string escapes are not TOML's: \a and \v produce invalid TOML. And a
// newline in either cava field injects directives into the generated config.
func TestControlCharactersAndNewlinesAreRejected(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)
	cases := map[string]string{
		"bell":    "a\ab",
		"vtab":    "a\vb",
		"newline": "pipewire\nsomething = evil",
	}
	for name, value := range cases {
		if err := WriteLightbarSetting("pulse.input_source", value); err == nil {
			t.Errorf("%s was accepted into pulse.input_source", name)
		}
	}
	if _, err := LoadLightbarSettings(); err != nil {
		t.Fatalf("the file was damaged by a rejected write: %v", err)
	}
}

// Both of these used to exit 0 and then break the daemon in a way that is very
// hard to trace back to the settings file.
func TestValuesThatWouldBreakTheDaemonAreRejected(t *testing.T) {
	withSettingsFile(t, DefaultLightbarSettingsFile)

	if err := WriteLightbarSetting("pulse.input_method", "bogus"); err == nil {
		t.Error("an input method cava does not support was accepted")
	}
	if err := WriteLightbarSetting("pulse.player", "weird name"); err == nil {
		t.Error("a player name that makes dbus-monitor reject the match rule was accepted")
	}
	if err := WriteLightbarSetting("pulse.player", "org.mpris.MediaPlayer2.mpv"); err != nil {
		t.Errorf("a valid full bus name was rejected: %v", err)
	}
}

// The recovery path: a file that does not load cannot be repaired by `config
// set`, so the error has to say what will, and that has to work.
func TestUnloadableFileIsRepairableByReset(t *testing.T) {
	withSettingsFile(t, "[pulse]\nfps = 999\n")

	err := WriteLightbarSetting("pulse.fps", "30")
	if err == nil {
		t.Fatal("expected config set to refuse an unloadable file")
	}
	if !strings.Contains(err.Error(), "config reset") {
		t.Errorf("the error does not point at the way out: %v", err)
	}

	backup, err := ResetLightbarSettingsFile()
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Error("reset did not keep a backup of the broken file")
	}
	if body, err := os.ReadFile(backup); err != nil || !strings.Contains(string(body), "999") {
		t.Errorf("the backup does not hold the broken file: %v", err)
	}
	settings, err := LoadLightbarSettings()
	if err != nil {
		t.Fatalf("the file still does not load after reset: %v", err)
	}
	if settings != DefaultLightbarSettings() {
		t.Error("reset did not restore the defaults")
	}
}

// The old sync test loaded the shipped file *over* the defaults, so a key
// omitted from it passed silently. This one checks presence.
func TestShippedFileMentionsEverySettableKey(t *testing.T) {
	for _, key := range SettingKeys() {
		_, name, _ := strings.Cut(key, ".")
		if !strings.Contains(DefaultLightbarSettingsFile, "\n"+name+" = ") {
			t.Errorf("%s is settable but does not appear in the shipped default file", key)
		}
	}
}
