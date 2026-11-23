package livegame

import (
	"embed"
	"encoding/base64"
	"fmt"
	"strings"
)

//go:embed assets/bungee.woff2 assets/exo2.woff2
var fontFS embed.FS

// FontFaceCSS is the two TV fonts as @font-face rules with the fonts inlined
// as base64 data URIs: Bungee (display) and Exo 2 (body).
//
// Inlined because a TV page is a kiosk stick on bar wifi. A page that falls
// back to a system font loses the size calibration the whole 1920x1080 layout
// is built around, and a font fetched separately is one more thing the first
// network hiccup can take away.
func FontFaceCSS() (string, error) {
	bungee, err := fontDataURI("assets/bungee.woff2")
	if err != nil {
		return "", err
	}
	exo, err := fontDataURI("assets/exo2.woff2")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@font-face{font-family:bungee;src:url(%s) format(\"woff2\");"+
		"font-weight:400;font-style:normal;font-display:block}\n", bungee)
	fmt.Fprintf(&b, "@font-face{font-family:exo2;src:url(%s) format(\"woff2\");"+
		"font-weight:400 700;font-style:normal;font-display:block}\n", exo)
	return b.String(), nil
}

func fontDataURI(name string) (string, error) {
	raw, err := fontFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("reading embedded font %s: %w", name, err)
	}
	return "data:font/woff2;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// DisplayHost strips the scheme from a join URL for printing on the wall:
// somebody typing it into a phone does not type "https://", and the shorter
// string sets bigger.
func DisplayHost(join string) string {
	s := strings.TrimPrefix(join, "https://")
	return strings.TrimPrefix(s, "http://")
}
