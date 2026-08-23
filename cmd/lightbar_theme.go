package cmd

import (
	"fmt"

	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// themeLightbarColor resolves --theme to a colour from the applied Omarchy
// theme.
//
// The rule used to live as an awk script inside the theme-set hook. It moved
// here because the pulse daemon needs the same choice in memory and needs it
// re-made on every theme switch; keeping two implementations of "which colour
// is the theme's" would have let them drift silently apart. The hook now calls
// this, so there is one answer.
func themeLightbarColor(colorKey string) (string, error) {
	palette, err := omarchy.CurrentPalette()
	if err != nil {
		return "", fmt.Errorf("reading the current Omarchy theme: %w", err)
	}

	if colorKey != "" && colorKey != "auto" {
		colors, err := omarchy.ReadColors(omarchy.ColorsPath())
		if err != nil {
			return "", err
		}
		color, ok := colors[colorKey]
		if !ok {
			return "", fmt.Errorf("the current theme defines no %q colour", colorKey)
		}
		return color.Hex(), nil
	}

	return palette.Mid.Hex(), nil
}
