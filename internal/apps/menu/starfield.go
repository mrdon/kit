package menu

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"strings"
)

// The starfield is generated rather than shipped as an image file, so it costs
// a few KB of markup instead of an asset, and it is generated ONCE at package
// load rather than per render: a wall display that reloads should come back to
// the same sky, not a different one. A fixed seed is what makes that true.
const (
	starSeed   = 20260831
	starWidth  = 1920
	starHeight = 1080

	// Enough to read as a sky at ten feet without turning the ground into
	// noise behind 46px type. Most of these sit under the tap list, so the
	// ceiling on brightness matters more than the count.
	starCount  = 240
	brightStar = 9
)

// starfieldURI is the background layer, built once at startup.
var starfieldURI = buildStarfield()

// buildStarfield draws a deterministic star pattern as an inline SVG.
//
// Base64 rather than raw SVG in the url(): a raw SVG data URI has to escape
// '#', '<' and '"' correctly for both CSS and the browser's URL parser, and
// getting one character wrong yields a silently blank background rather than
// an error. The encoding costs about a third more bytes and removes the whole
// class of problem.
func buildStarfield() string {
	rng := rand.New(rand.NewSource(starSeed)) //nolint:gosec // decorative, not cryptographic

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d">`, starWidth, starHeight, starWidth, starHeight)

	for i := 0; i < starCount; i++ {
		x := rng.Float64() * starWidth
		y := rng.Float64() * starHeight
		// Radius and opacity move together so the small ones recede rather
		// than reading as evenly-spaced dust.
		r := 0.4 + rng.Float64()*1.0
		o := 0.10 + rng.Float64()*0.42
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="#fff" opacity="%.2f"/>`, x, y, r, o)
	}

	// A handful of brighter stars with a soft halo. Without these the field
	// is uniform, and a uniform field reads as texture rather than as sky.
	for i := 0; i < brightStar; i++ {
		x := rng.Float64() * starWidth
		y := rng.Float64() * starHeight
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="#ffd9c9" opacity="0.16"/>`,
			x, y, 3.0+rng.Float64()*2.5)
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="1.3" fill="#fff" opacity="0.75"/>`, x, y)
	}

	b.WriteString(`</svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String()))
}
