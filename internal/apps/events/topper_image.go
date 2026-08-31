package events

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the GIF decoder for image.Decode
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode
	"log/slog"

	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder for image.Decode

	"github.com/mrdon/kit/internal/attachment"
)

// Getting an event poster into a PDF.
//
// Posters are authored for the website, so they arrive as whatever a phone or
// a designer produced: a 4000px JPEG, a PNG logo on transparency, a WebP
// export. The topper needs the opposite of all that -- a small square that can
// be dropped into a circle on a coloured band -- so this file normalises them
// to one shape once, rather than making the PDF writer cope with four.
//
// Three things happen here, and each is load-bearing:
//
//   - Transcode. fpdf embeds JPEG, PNG and GIF; WebP would otherwise have to
//     be rejected at upload, which would be the print path dictating the
//     website's format choice.
//   - Downscale. Seven bands of untouched 8MB uploads is a 50MB PDF that a
//     taproom laptop will not print.
//   - Flatten onto white. JPEG has no alpha, and a transparent logo composited
//     onto black would print as a dark blob. White also matches the design:
//     the thumbnail reads as a white coin on the band.
const posterThumbPx = 600

// posterThumbQuality trades a little fidelity for a printable file. The
// thumbnail lands about 20mm wide, where JPEG artefacts are invisible.
const posterThumbQuality = 82

// loadPoster fetches and normalises one poster, or returns nil.
//
// Every failure path returns nil rather than an error. A missing thumbnail
// costs a band some colour; a failed print run costs the taproom its tables.
func loadPoster(ctx context.Context, store *attachment.Service, tenantID, id uuid.UUID) *posterImage {
	meta, raw, err := store.Load(ctx, tenantID, id)
	if err != nil {
		slog.Warn("events topper: loading poster", "attachment_id", id, "error", err)
		return nil
	}
	img, err := posterThumb(raw)
	if err != nil {
		slog.Warn("events topper: decoding poster",
			"attachment_id", id, "mime", meta.Mime, "error", err)
		return nil
	}
	return img
}

// posterThumb turns arbitrary poster bytes into a small square JPEG.
//
// The crop is centred, which is the right default for artwork: posters put
// their subject in the middle, and a circular mask crops the corners away
// regardless.
func posterThumb(raw []byte) (*posterImage, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decoding poster: %w", err)
	}
	square := centreSquare(src.Bounds())
	side := min(square.Dx(), posterThumbPx)

	dst := image.NewRGBA(image.Rect(0, 0, side, side))
	// White first, then the image over it: this is the alpha flatten, and it
	// has to happen before the JPEG encode rather than being left to it.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, square, xdraw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: posterThumbQuality}); err != nil {
		return nil, fmt.Errorf("encoding poster thumbnail: %w", err)
	}
	return &posterImage{Data: buf.Bytes(), Kind: "JPG"}, nil
}

// centreSquare is the largest square crop centred in b.
func centreSquare(b image.Rectangle) image.Rectangle {
	side := min(b.Dx(), b.Dy())
	x := b.Min.X + (b.Dx()-side)/2
	y := b.Min.Y + (b.Dy()-side)/2
	return image.Rect(x, y, x+side, y+side)
}
