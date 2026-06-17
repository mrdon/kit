package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register GIF decoder for image.Decode
	"image/jpeg"  // JPEG encode + decoder registration
	_ "image/png" // register PNG decoder for image.Decode
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Sender is the subset of *Client that helpers need, so callers can stub
// the Messages API in tests. *Client satisfies it directly.
type Sender interface {
	CreateMessage(ctx context.Context, req *Request) (*Response, error)
}

const (
	// visionMaxDim is the longest-edge pixel cap. The Claude API degrades
	// images past ~1568px on the long edge, so downscaling there bounds
	// token cost and keeps the base64 payload well under the 10MB image
	// limit (a 12MP phone photo becomes a few hundred KB).
	visionMaxDim = 1568
	// visionMaxTokens caps the extracted-text response.
	visionMaxTokens = 1500
	// visionJPEGQuality balances fidelity against payload size.
	visionJPEGQuality = 85
)

// defaultVisionInstructions is used when the caller does not target the
// extraction. Receipt/expense callers pass their own (e.g. "extract
// vendor, date, amount, tax, line items").
const defaultVisionInstructions = "Transcribe all text in this image faithfully and completely, then briefly describe what the image shows."

// DescribeImage downscales an image and asks Sonnet to return text about
// it (a faithful transcription plus description, or whatever instructions
// target). It returns the model's text — the raw image never leaves this
// call, so callers (e.g. the read_attachment tool) can hand the result
// straight into the existing string-based tool/agent plumbing.
func DescribeImage(ctx context.Context, sender Sender, imageBytes []byte, mimeType, instructions string) (string, error) {
	if strings.TrimSpace(instructions) == "" {
		instructions = defaultVisionInstructions
	}

	jpegBytes, err := downscaleToJPEG(imageBytes, visionMaxDim)
	if err != nil {
		return "", fmt.Errorf("preparing image: %w", err)
	}

	resp, err := sender.CreateMessage(ctx, &Request{
		Model:     ModelSonnet,
		MaxTokens: visionMaxTokens,
		Messages: []Message{{
			Role: "user",
			Content: []Content{
				ImageContent("image/jpeg", jpegBytes),
				{Type: "text", Text: instructions},
			},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("vision request: %w", err)
	}

	text := strings.TrimSpace(resp.TextContent())
	if text == "" {
		return "", errors.New("vision response contained no text")
	}
	return text, nil
}

// downscaleToJPEG decodes an image (JPEG/PNG/GIF), resizes it so neither
// edge exceeds maxDim (preserving aspect ratio; never upscaling), and
// re-encodes as JPEG. Re-encoding normalizes the media type and keeps the
// payload well under the API's 10MB base64 image cap.
func downscaleToJPEG(data []byte, maxDim int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("image has a zero dimension")
	}

	nw, nh := targetDims(w, h, maxDim)
	out := img
	if nw != w || nh != h {
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
		out = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: visionJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encoding jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// targetDims returns the largest dimensions that fit within maxDim on both
// edges while preserving aspect ratio. It never upscales.
func targetDims(w, h, maxDim int) (int, int) {
	if w <= maxDim && h <= maxDim {
		return w, h
	}
	if w >= h {
		nh := int(float64(h) * float64(maxDim) / float64(w))
		return maxDim, max(nh, 1)
	}
	nw := int(float64(w) * float64(maxDim) / float64(h))
	return max(nw, 1), maxDim
}
