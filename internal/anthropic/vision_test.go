package anthropic

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// fakeSender records the request it received and returns a canned response.
type fakeSender struct {
	got  *Request
	resp *Response
	err  error
}

func (f *fakeSender) CreateMessage(_ context.Context, req *Request) (*Response, error) {
	f.got = req
	return f.resp, f.err
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func TestTargetDims(t *testing.T) {
	cases := []struct {
		w, h, max    int
		wantW, wantH int
	}{
		{100, 100, 1568, 100, 100},     // already small — unchanged
		{3000, 2000, 1568, 1568, 1045}, // landscape
		{2000, 3000, 1568, 1045, 1568}, // portrait
		{1568, 1568, 1568, 1568, 1568}, // exactly at the cap
	}
	for _, c := range cases {
		gw, gh := targetDims(c.w, c.h, c.max)
		if gw != c.wantW || gh != c.wantH {
			t.Errorf("targetDims(%d,%d,%d) = %d,%d; want %d,%d",
				c.w, c.h, c.max, gw, gh, c.wantW, c.wantH)
		}
	}
}

func TestDownscaleToJPEG(t *testing.T) {
	src := pngBytes(t, 3000, 2000)
	out, err := downscaleToJPEG(src, visionMaxDim)
	if err != nil {
		t.Fatalf("downscaleToJPEG: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q; want jpeg", format)
	}
	if cfg.Width > visionMaxDim || cfg.Height > visionMaxDim {
		t.Errorf("downscaled to %dx%d; want both <= %d", cfg.Width, cfg.Height, visionMaxDim)
	}
	if cfg.Width != visionMaxDim {
		t.Errorf("long edge = %d; want %d", cfg.Width, visionMaxDim)
	}
}

func TestDownscaleToJPEG_Invalid(t *testing.T) {
	if _, err := downscaleToJPEG([]byte("not an image"), visionMaxDim); err == nil {
		t.Fatal("expected error decoding non-image bytes")
	}
}

func TestDescribeImage(t *testing.T) {
	fs := &fakeSender{resp: &Response{
		Content: []Content{{Type: "text", Text: "  Vendor: ACME Co\nTotal: $42.50  "}},
	}}

	got, err := DescribeImage(context.Background(), fs, pngBytes(t, 200, 150), "image/png", "extract vendor and total")
	if err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if got != "Vendor: ACME Co\nTotal: $42.50" {
		t.Errorf("text = %q; want trimmed vendor/total", got)
	}

	// Request shape: Sonnet, one user message of [image, text].
	if fs.got.Model != ModelSonnet {
		t.Errorf("model = %q; want %q", fs.got.Model, ModelSonnet)
	}
	if len(fs.got.Messages) != 1 || len(fs.got.Messages[0].Content) != 2 {
		t.Fatalf("unexpected message shape: %+v", fs.got.Messages)
	}
	img := fs.got.Messages[0].Content[0]
	if img.Type != "image" || img.Source == nil || img.Source.MediaType != "image/jpeg" || img.Source.Data == "" {
		t.Errorf("first block not a base64 jpeg image: %+v", img)
	}
	if txt := fs.got.Messages[0].Content[1]; txt.Type != "text" || txt.Text != "extract vendor and total" {
		t.Errorf("second block = %+v; want the instructions text", txt)
	}
}

func TestDescribeImage_DefaultInstructions(t *testing.T) {
	fs := &fakeSender{resp: &Response{Content: []Content{{Type: "text", Text: "ok"}}}}
	if _, err := DescribeImage(context.Background(), fs, pngBytes(t, 32, 32), "image/png", "   "); err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if fs.got.Messages[0].Content[1].Text != defaultVisionInstructions {
		t.Errorf("blank instructions should fall back to the default")
	}
}

func TestDescribeImage_EmptyResponse(t *testing.T) {
	fs := &fakeSender{resp: &Response{Content: []Content{{Type: "text", Text: "   "}}}}
	if _, err := DescribeImage(context.Background(), fs, pngBytes(t, 32, 32), "image/png", ""); err == nil {
		t.Fatal("expected error on empty vision response")
	}
}
