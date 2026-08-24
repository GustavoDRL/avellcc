package cmd

import (
	"fmt"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// themeColor resolves --theme to a colour from the applied Omarchy theme.
//
// The rule used to live as an awk script inside the theme-set hook. It moved
// here because the pulse daemon needs the same choice in memory and needs it
// re-made on every theme switch; keeping two implementations of "which colour
// is the theme's" would have let them drift silently apart. The hooks now call
// this, so there is one answer.
//
// `lightbar --theme` and `keyboard --theme` both come through here, for the
// same reason: the owner's rule is that the wallpaper decides the colour, and
// the wallpaper reaches avellcc as the accent override. Two resolvers would be
// two chances for one of them to stop honouring it.
func themeColor(colorKey string) (string, error) {
	// CurrentColors, not ReadColors: the override has to be in the map before
	// it is indexed, or `color_key = "accent"` silently ignores it. It also
	// used to parse the file twice per call, once here and once for the
	// palette.
	colors, err := omarchy.CurrentColors()
	if err != nil {
		return "", fmt.Errorf("reading the current Omarchy theme: %w", err)
	}

	if colorKey != "" && colorKey != "auto" {
		color, ok := colors[colorKey]
		if !ok {
			return "", fmt.Errorf("the current theme defines no %q colour", colorKey)
		}
		return color.Hex(), nil
	}

	return omarchy.PaletteFrom(colors).Mid.Hex(), nil
}
