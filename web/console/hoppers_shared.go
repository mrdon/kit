package console

import (
	"embed"
	"fmt"
)

// hoppersShared is the source of what the Hoppers TV and phone share: the
// frog art, the HOP constants and the theme. The phone imports these through
// Vite; the TV page has no build step, so Go embeds the same files from here
// and inlines them (internal/apps/hoppers/render.go). One copy, two readers.
//
//go:embed src/hoppers/art/*.js src/hoppers/consts.js src/hoppers/theme.css
var hoppersShared embed.FS

// HoppersShared returns one shared Hoppers source file by its path under
// src/hoppers/ ("art/frog.js", "consts.js", "theme.css").
func HoppersShared(name string) ([]byte, error) {
	b, err := hoppersShared.ReadFile("src/hoppers/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading shared hoppers file %s: %w", name, err)
	}
	return b, nil
}
