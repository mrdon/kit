package trivia

import (
	"strings"
	"testing"
)

// The QR is scanned from across a bar, so the two things that matter are that
// it encodes the right URL and that it carries a generous quiet zone.
func TestRenderQRProducesScalableSVG(t *testing.T) {
	svg, err := RenderQR("https://kit.example.com/acme/trivia/brave-otter-lamp", 640)
	if err != nil {
		t.Fatalf("RenderQR: %v", err)
	}
	s := string(svg)
	if !strings.HasPrefix(s, "<svg ") || !strings.HasSuffix(s, "</svg>") {
		t.Fatalf("not an svg element: %.80s", s)
	}
	if !strings.Contains(s, `width="640" height="640"`) {
		t.Fatal("requested size is not on the root element")
	}
	if !strings.Contains(s, "viewBox=") {
		t.Fatal("no viewBox — the code would not scale to the panel")
	}
	if !strings.Contains(s, `fill="#ffffff"`) {
		t.Fatal("no white ground — the quiet zone would inherit the page background and stop scanning")
	}
	if strings.Count(s, "<rect") < 10 {
		t.Fatalf("only %d rects — the module grid did not render", strings.Count(s, "<rect"))
	}
}

// Longer URLs need a denser code; it must still encode rather than error.
func TestRenderQRHandlesLongURLs(t *testing.T) {
	long := "https://a-fairly-long-workspace-hostname.example.com/some-long-workspace-slug/trivia/brave-otter-lamp"
	if _, err := RenderQR(long, 640); err != nil {
		t.Fatalf("RenderQR on a long URL: %v", err)
	}
}

func TestJoinURL(t *testing.T) {
	cases := map[string]string{
		"https://kit.example.com":  "https://kit.example.com/acme/trivia/brave-otter-lamp",
		"https://kit.example.com/": "https://kit.example.com/acme/trivia/brave-otter-lamp",
	}
	for base, want := range cases {
		if got := JoinURL(base, "acme", "brave-otter-lamp"); got != want {
			t.Errorf("JoinURL(%q) = %q, want %q", base, got, want)
		}
	}
}
