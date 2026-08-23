//go:build ignore || probe

// Command gridpaint lights LED grid positions directly, with no key map in the
// way. It is the ground-truth tool for working out which physical key sits
// under which grid position.
//
//	gridpaint row=0:ff0000 row=2:00ff00        # whole rows
//	gridpaint 3,8:ffffff                       # one position
//	gridpaint col=14:ff00ff                    # a whole column
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hugo-andrade/avellcc/internal/keyboard"
)

func parseHex(s string) ([3]byte, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return [3]byte{}, fmt.Errorf("expected RRGGBB, got %q", s)
	}
	var out [3]byte
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return [3]byte{}, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gridpaint [row=N:RRGGBB | col=N:RRGGBB | R,C:RRGGBB]...")
		os.Exit(2)
	}

	ctrl, err := keyboard.NewController()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := ctrl.Open(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = ctrl.Close() }()

	_ = ctrl.SetBrightness(10)
	colorMap := map[[2]int][3]byte{}
	for r := 0; r < ctrl.Rows(); r++ {
		for c := 0; c < ctrl.Cols(); c++ {
			colorMap[[2]int{r, c}] = [3]byte{0, 0, 0}
		}
	}

	for _, arg := range os.Args[1:] {
		spec, hex, ok := strings.Cut(arg, ":")
		if !ok {
			fmt.Fprintf(os.Stderr, "bad argument %q\n", arg)
			os.Exit(2)
		}
		rgb, err := parseHex(hex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad colour in %q: %v\n", arg, err)
			os.Exit(2)
		}

		switch {
		case strings.HasPrefix(spec, "row="):
			n, _ := strconv.Atoi(strings.TrimPrefix(spec, "row="))
			for c := 0; c < ctrl.Cols(); c++ {
				colorMap[[2]int{n, c}] = rgb
			}
			fmt.Printf("row %d -> #%s\n", n, hex)
		case strings.HasPrefix(spec, "col="):
			n, _ := strconv.Atoi(strings.TrimPrefix(spec, "col="))
			for r := 0; r < ctrl.Rows(); r++ {
				colorMap[[2]int{r, n}] = rgb
			}
			fmt.Printf("col %d -> #%s\n", n, hex)
		default:
			rs, cs, ok := strings.Cut(spec, ",")
			if !ok {
				fmt.Fprintf(os.Stderr, "bad position %q\n", spec)
				os.Exit(2)
			}
			r, _ := strconv.Atoi(rs)
			c, _ := strconv.Atoi(cs)
			colorMap[[2]int{r, c}] = rgb
			fmt.Printf("row %d col %d -> #%s\n", r, c, hex)
		}
	}

	if err := ctrl.SetKeyMap(colorMap); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
