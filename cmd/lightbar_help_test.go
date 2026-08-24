package cmd

import (
	"strings"
	"testing"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
)

// `avellcc lightbar --help` used to print the ITE 8911 vocabulary and nothing
// else, on every machine. On this laptop — an ITE 8233 chassis bar — that help
// advertised `breathe`, which the command rejects, hid `bounce`, `marquee` and
// `scan`, which it accepts, and called the brightness range 0-4 when 4 means 4
// of 100. The help text cannot detect the controller (init runs first), so it
// has to name both sets; these tests are driven by the effect maps themselves,
// so a new effect on either controller that never reaches the help is red.
func TestLightbarHelpNamesBothControllers(t *testing.T) {
	usage := func(name string) string {
		f := lightbarCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("flag --%s disappeared", name)
		}
		return f.Usage
	}

	effect := usage("effect")
	for name := range lightbar.Effects8233 {
		if !strings.Contains(effect, name) {
			t.Errorf("--effect help does not mention the ITE 8233 effect %q: %s", name, effect)
		}
	}
	for name := range lightbar.X58EffectCodes {
		if !strings.Contains(effect, name) {
			t.Errorf("--effect help does not mention the ITE 8911 effect %q: %s", name, effect)
		}
	}
	if !strings.Contains(effect, "8233") || !strings.Contains(effect, "8911") {
		t.Errorf("--effect help does not say which set belongs to which controller: %s", effect)
	}

	bright := usage("brightness")
	if !strings.Contains(bright, "0-4") {
		t.Errorf("--brightness help lost the ITE 8911 range: %s", bright)
	}
	if !strings.Contains(bright, "0-100") {
		t.Errorf("--brightness help does not carry the ITE 8233 range 0-%d: %s",
			lightbar.MaxBrightness8233, bright)
	}

	color := usage("color")
	if !strings.Contains(color, "#RRGGBB") {
		t.Errorf("--color help hides that the ITE 8233 takes an arbitrary colour: %s", color)
	}
}

// The flag default and the settings file must be the same number. They were
// not: --pulse-gain said 1.25, the file and pulse.DefaultConfig said 2.0.
func TestPulseFlagDefaultsMatchTheSettingsFile(t *testing.T) {
	d := config.DefaultLightbarSettings().Pulse
	f := lightbarCmd.Flags()

	for _, c := range []struct {
		flag string
		want string
	}{
		{"pulse-gain", "2"},
		{"pulse-fps", "30"},
		{"pulse-min-brightness", "12"},
		{"pulse-max-brightness", "100"},
		{"pulse-input-method", d.InputMethod},
		{"pulse-input-source", d.InputSource},
	} {
		got := f.Lookup(c.flag).DefValue
		if got != c.want {
			t.Errorf("--%s default is %q, but the settings file default is %q", c.flag, got, c.want)
		}
	}

	// And the literals above are only right while they agree with the struct,
	// which is the value the daemon actually uses.
	if d.Gain != 2.0 || d.FPS != 30 || d.MinBrightness != 12 || d.MaxBrightness != 100 {
		t.Fatalf("DefaultLightbarSettings().Pulse moved: %+v — update this test with it", d)
	}
}
