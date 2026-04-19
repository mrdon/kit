package slack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// maxIconBytes caps icon downloads. Slack icons are well under this; the
// limit is a defense against a malicious or compromised Slack response
// pointing at a gigantic file.
const maxIconBytes = 1 << 20 // 1 MiB

// iconAllowedHosts is the set of CDN hostnames Slack serves workspace
// icons from. Exact-match for slack.com; suffix-match for slack-edge.com
// and other subdomains. Anything else is refused — SSRF defense.
var iconAllowedHosts = []string{
	"slack.com",
	".slack.com",
	".slack-edge.com",
}

// fetchSlackIcon downloads an icon URL returned by team.info. It enforces
// a host allowlist, bounded redirects that re-validate the host, a short
// timeout, a response-size cap, and a PNG magic-byte check. On any failure
// returns (nil, err) — callers should treat that as "no icon" rather than
// failing the install.
func fetchSlackIcon(ctx context.Context, iconURL string) ([]byte, error) {
	if iconURL == "" {
		return nil, nil
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return validateIconHost(req.URL)
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, iconURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building icon request: %w", err)
	}
	if err := validateIconHost(req.URL); err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching icon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("icon status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading icon body: %w", err)
	}
	if len(body) > maxIconBytes {
		return nil, errors.New("icon body exceeds size cap")
	}
	if !isPNG(body) {
		return nil, errors.New("icon is not a PNG")
	}
	return body, nil
}

func validateIconHost(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("icon URL scheme %q not allowed", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		return errors.New("icon URL uses raw IP")
	}
	for _, allowed := range iconAllowedHosts {
		if strings.HasPrefix(allowed, ".") {
			if strings.HasSuffix(host, allowed) {
				return nil
			}
			continue
		}
		if host == allowed {
			return nil
		}
	}
	return fmt.Errorf("icon host %q not allowed", host)
}

// isPNG checks the 8-byte PNG magic.
func isPNG(b []byte) bool {
	const pngMagic = "\x89PNG\r\n\x1a\n"
	return len(b) >= len(pngMagic) && string(b[:len(pngMagic)]) == pngMagic
}

// ResizePNGSquare decodes `src` as a PNG and re-encodes it as a square
// PNG of `size`x`size` pixels using high-quality CatmullRom filtering.
// Returns (nil, nil) if src is empty — callers can treat NULL icon as
// "no bytes stored" and skip without erroring.
//
// Slack's `team.info` caps at 230x230, so PWA manifests that declare
// 192x192 or 512x512 would otherwise be rejected by Firefox as too
// small for home-screen install.
func ResizePNGSquare(src []byte, size int) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decoding source png: %w", err)
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Rect, img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("encoding resized png: %w", err)
	}
	return out.Bytes(), nil
}
