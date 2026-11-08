package style

import (
	"fmt"
	"slices"
	"strings"
)

// ColorMode is when text output is colored.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto" // when writing to a terminal
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// ColorModes is every ColorMode.
var ColorModes = []ColorMode{ColorAuto, ColorAlways, ColorNever}

// ParseColorMode returns the mode named v.
func ParseColorMode(v string) (ColorMode, error) {
	if m := ColorMode(strings.ToLower(v)); slices.Contains(ColorModes, m) {
		return m, nil
	}
	return "", fmt.Errorf("%q is not one of auto, always, never", v)
}
