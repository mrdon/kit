package events

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log/slog"

	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"

	"github.com/mrdon/kit/internal/attachment"
)

// Posters for the entrance sign. Unlike the topper's coin, these keep their
// shape: a poster cropped to a square loses its own headline, and the sign
// has the room to show it whole. Same normalising otherwise (transcode,
// downscale, flatten onto white), for the same reasons; see topper_image.go.

const (
	// signThumbPx is a poster beside an event's text, about 60mm on paper.
	signThumbPx = 900
	// signPosterPx is a poster printed as the whole page.
	signPosterPx = 2400
)

// signImage is a poster ready for fpdf, with its pixel size so it can be
// fitted into a box without distortion.
type signImage struct {
	Data []byte
	W, H int
}

// loadSignImage fetches and normalises one poster, or returns nil. A missing
// picture costs the sign a picture, never the sign.
func loadSignImage(ctx context.Context, store *attachment.Service, tenantID, id uuid.UUID, maxPx int) *signImage {
	meta, raw, err := store.Load(ctx, tenantID, id)
	if err != nil {
		slog.Warn("events sign: loading poster", "attachment_id", id, "error", err)
		return nil
	}
	img, err := signFit(raw, maxPx)
	if err != nil {
		slog.Warn("events sign: decoding poster", "attachment_id", id, "mime", meta.Mime, "error", err)
		return nil
	}
	return img
}

// signFit scales a poster so its longer side is at most maxPx, keeping its
// shape, and re-encodes it as a JPEG on white.
func signFit(raw []byte, maxPx int) (*signImage, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decoding poster: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("poster has no pixels")
	}
	if long := max(w, h); long > maxPx {
		w, h = max(w*maxPx/long, 1), max(h*maxPx/long, 1)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encoding poster: %w", err)
	}
	return &signImage{Data: buf.Bytes(), W: w, H: h}, nil
}

// fitBox is the largest w x h with the image's shape inside a box, centred.
func (s *signImage) fitBox(x, y, boxW, boxH float64) (fx, fy, fw, fh float64) {
	scale := min(boxW/float64(s.W), boxH/float64(s.H))
	fw, fh = float64(s.W)*scale, float64(s.H)*scale
	return x + (boxW-fw)/2, y + (boxH-fh)/2, fw, fh
}
