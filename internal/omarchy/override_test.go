package omarchy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHex(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want RGB
		ok   bool
	}{
		{"#FFB6D1", RGB{0xFF, 0xB6, 0xD1}, true},
		{"ffb6d1", RGB{0xFF, 0xB6, 0xD1}, true},
		{"  #10ABA2\n", RGB{0x10, 0xAB, 0xA2}, true},
		{"#000000", RGB{0, 0, 0}, true},
		{"#fff", RGB{}, false},
		{"", RGB{}, false},
		{"#gggggg", RGB{}, false},
	} {
		got, ok := ParseHex(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseHex(%q) = %v,%v; want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestAccentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accent-override")
	t.Setenv("AVELLCC_ACCENT_OVERRIDE", path)

	if _, ok := AccentOverride(); ok {
		t.Fatal("no file: expected no override in force")
	}

	if err := os.WriteFile(path, []byte("#10ABA2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := AccentOverride()
	if !ok || got != (RGB{0x10, 0xAB, 0xA2}) {
		t.Fatalf("got %v,%v; want #10ABA2,true", got, ok)
	}

	// Lixo no arquivo não deve derrubar a cor do tema.
	if err := os.WriteFile(path, []byte("not a colour"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := AccentOverride(); ok {
		t.Error("malformed file: expected no override in force")
	}
}

// A cor parkeada tem de chegar às três bandas, não só ao bass: mid e treble
// são derivados do accent, então trocá-lo move as três.
func TestCurrentPaletteHonoursOverride(t *testing.T) {
	colors := map[string]RGB{
		"accent": {0xFF, 0xB6, 0xD1},
		"green":  {0x6E, 0x8F, 0x7A},
		"blue":   {0x8F, 0xA0, 0xC4},
	}
	before := PaletteFrom(colors)
	colors["accent"] = RGB{0x10, 0xAB, 0xA2}
	after := PaletteFrom(colors)

	if before.Bass == after.Bass {
		t.Error("bass should follow the accent")
	}
}
