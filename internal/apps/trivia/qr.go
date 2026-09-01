package trivia

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// quietZone is the blank margin around the code, in modules. The spec's
// minimum is 4; this is deliberately generous because the code is being
// scanned from across a bar. TV panels blur, phone cameras hunt for focus in
// low light, and a tight QR simply will not scan from fifteen feet.
const quietZone = 6

// RenderQR draws a join URL as inline SVG.
//
// SVG rather than a PNG data URI because the TV display scales a fixed
// 1920x1080 stage to whatever panel it lands on, and a raster would resample
// to blur at exactly the moment scanning gets hard. Scan rate from fifteen
// feet measurably depends on edge sharpness.
//
// size is the rendered edge length in stage pixels. The returned value is
// injected into the template as template.HTML, so it must never carry
// untrusted markup -- everything below is generated from a module grid.
func RenderQR(url string, size int) (template.HTML, error) {
	code, err := qr.Encode(url, qr.M)
	if err != nil {
		return "", fmt.Errorf("encoding qr for %q: %w", url, err)
	}
	modules := code.Size
	total := modules + 2*quietZone

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="Join code">`,
		size, size, total, total)
	// The quiet zone is part of the code, not padding around it: a scanner
	// needs the light margin to find the finder patterns, so it is painted
	// white here rather than left to whatever the page background happens to
	// be.
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#ffffff"/>`, total, total)

	// One rect per run of dark modules in a row rather than one per module.
	// A 33x33 code is about a thousand elements drawn naively and roughly a
	// tenth of that as runs, which matters on the cheap stick driving the TV.
	for y := range modules {
		x := 0
		for x < modules {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 0
			for x+run < modules && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="1" fill="#000000"/>`,
				x+quietZone, y+quietZone, run)
			x += run
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), nil //nolint:gosec // generated from a module grid, no untrusted input
}

// JoinURL is the address a phone lands on after scanning, and the one a host
// reads out. Built from the configured base URL so the console and the TV can
// never disagree about where a game lives.
func JoinURL(baseURL, slug, gameName string) string {
	return strings.TrimRight(baseURL, "/") + "/" + slug + "/trivia/" + gameName
}
