package omarchy

import (
	"os"
	"path/filepath"
	"strings"
)

// An accent override lets something outside avellcc borrow the bar and the
// keyboard for a while — the case this was written for is a theme whose colours
// follow the album that is playing, where the accent changes per album but the
// applied Omarchy theme does not.
//
// The contract is deliberately a file and not a flag or a socket: the writer
// may be a shell script, the reader is a daemon that already re-reads its
// settings once a second, and neither has to know the other exists. Absent
// file, or unreadable, or malformed — the theme's own accent stands.
//
// The writer is responsible for removing it. A stale override is visible
// (`avellcc lightbar config show` reports it) and costs nothing but a colour.

// AccentOverridePath is where a colour may be parked to stand in for the
// theme's accent. $AVELLCC_ACCENT_OVERRIDE wins, so a caller can point it
// somewhere else without touching this.
func AccentOverridePath() string {
	if p := os.Getenv("AVELLCC_ACCENT_OVERRIDE"); p != "" {
		return p
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "avellcc", "accent-override")
}

// AccentOverride reads the parked colour. The bool reports whether one is in
// force, so an unset override and a black override stay distinguishable.
func AccentOverride() (RGB, bool) {
	path := AccentOverridePath()
	if path == "" {
		return RGB{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return RGB{}, false
	}
	return ParseHex(strings.TrimSpace(string(raw)))
}

// ParseHex accepts "#rrggbb" or "rrggbb".
func ParseHex(s string) (RGB, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 6 {
		return RGB{}, false
	}
	var out RGB
	for i := 0; i < 3; i++ {
		var v int
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			var d int
			switch {
			case c >= '0' && c <= '9':
				d = int(c - '0')
			case c >= 'a' && c <= 'f':
				d = int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = int(c-'A') + 10
			default:
				return RGB{}, false
			}
			v = v*16 + d
		}
		out[i] = byte(v)
	}
	return out, true
}
