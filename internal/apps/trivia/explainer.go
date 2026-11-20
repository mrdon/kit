package trivia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The lobby video. A venue keeps a how-to-play video in Google Drive, shared
// as "anyone with the link", and the TV plays it beside the join code while
// people wait for the game to start.
//
// THE TV NEVER PLAYS FROM DRIVE DIRECTLY. Drive has no stable streaming URL
// for a <video> tag, answers a large file with a "can't scan this for
// viruses" page instead of the bytes, and throttles a popular file with "too
// many users have viewed". So Kit downloads the file, keeps it on local disk
// and serves it itself, with range requests, from /{slug}/trivia/explainer.mp4.
// The disk is a cache and nothing more: a restart empties it and the next
// screen to ask fills it again.
//
// Updating is done in Drive (Manage versions -> upload new version), which
// keeps the link. With a Drive API key Kit checks the file's modifiedTime
// every few minutes and downloads only when it moved; without one it cannot
// see that, so it simply downloads again every hour and keeps the old copy's
// timestamp when the bytes did not change.

const (
	// explainerMaxBytes refuses anything that is plainly not a short silent
	// loop. The how-to video is about 12MB; this leaves a lot of headroom.
	explainerMaxBytes = 300 << 20
	// How long a copy is trusted before Drive is asked again.
	explainerRecheckWithKey = 5 * time.Minute
	explainerRecheckNoKey   = time.Hour
	// A download outlives the request that started it, so a TV that gives up
	// halfway does not waste the fetch for the next one.
	explainerFetchTimeout = 3 * time.Minute
)

var (
	driveBareIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	driveFilePath = regexp.MustCompile(`/file/d/([A-Za-z0-9_-]{20,})`)
)

// driveFileID pulls the file id out of a pasted Drive link, or returns the
// input when it already is one. The error text is shown to the admin.
func driveFileID(link string) (string, error) {
	link = strings.TrimSpace(link)
	if driveBareIDRe.MatchString(link) {
		return link, nil
	}
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return "", errors.New("paste the Google Drive link to the video file")
	}
	host := strings.ToLower(u.Hostname())
	if host != "drive.google.com" && host != "docs.google.com" && host != "drive.usercontent.google.com" {
		return "", errors.New("that isn't a Google Drive link")
	}
	if strings.Contains(u.Path, "/folders/") {
		return "", errors.New("that's a folder; share the video file itself and paste its link")
	}
	if m := driveFilePath.FindStringSubmatch(u.Path); m != nil {
		return m[1], nil
	}
	if id := u.Query().Get("id"); driveBareIDRe.MatchString(id) {
		return id, nil
	}
	return "", errors.New("that Drive link doesn't name a file")
}

// explainerFile is one cached copy.
type explainerFile struct {
	path     string
	sum      string
	size     int64
	modified time.Time // what Last-Modified says; Drive's modifiedTime when known
	checked  time.Time
}

// explainerCache holds the downloaded videos, keyed by Drive file id. One
// mutex for the lot: a venue has one video, and a second screen asking while
// the first download runs should wait for it rather than start its own.
type explainerCache struct {
	dir        string
	apiKey     string
	apiBase    string // Drive v3 files endpoint, overridden in tests
	publicBase string // the no-key download endpoint, overridden in tests
	client     *http.Client
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]*explainerFile
}

func newExplainerCache(dir, apiKey string) *explainerCache {
	return &explainerCache{
		dir:        dir,
		apiKey:     apiKey,
		apiBase:    "https://www.googleapis.com/drive/v3/files/",
		publicBase: "https://drive.usercontent.google.com/download",
		client:     &http.Client{Timeout: explainerFetchTimeout},
		now:        time.Now,
		entries:    map[string]*explainerFile{},
	}
}

func (c *explainerCache) recheck() time.Duration {
	if c.apiKey != "" {
		return explainerRecheckWithKey
	}
	return explainerRecheckNoKey
}

// Get returns a local copy of the file, fetching or refreshing it as needed.
// Once a copy exists a failed refresh is logged and the old copy served: a
// Drive hiccup must not take the video off the wall.
func (c *explainerCache) Get(ctx context.Context, fileID string) (explainerFile, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), explainerFetchTimeout)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	have := c.entries[fileID]
	if have != nil && now.Sub(have.checked) < c.recheck() {
		return *have, nil
	}
	var driveModified time.Time
	if c.apiKey != "" {
		mod, err := c.modifiedTime(ctx, fileID)
		switch {
		case err != nil && have != nil:
			slog.Warn("checking trivia explainer video", "file_id", fileID, "error", err)
			have.checked = now
			return *have, nil
		case err != nil:
			return explainerFile{}, err
		case have != nil && mod.Equal(have.modified):
			have.checked = now
			return *have, nil
		}
		driveModified = mod
	}
	got, err := c.download(ctx, fileID)
	if err != nil {
		if have != nil {
			slog.Warn("refreshing trivia explainer video", "file_id", fileID, "error", err)
			have.checked = now
			return *have, nil
		}
		return explainerFile{}, err
	}
	got.checked = now
	got.modified = driveModified
	if got.modified.IsZero() {
		got.modified = now
		if have != nil && have.sum == got.sum {
			got.modified = have.modified // same bytes: keep browsers' copies valid
		}
	}
	if have != nil && have.path != got.path {
		_ = os.Remove(have.path)
	}
	c.entries[fileID] = &got
	return got, nil
}

// modifiedTime asks the Drive API when the file last changed, and confirms
// it is a video while it is there.
func (c *explainerCache) modifiedTime(ctx context.Context, fileID string) (time.Time, error) {
	q := url.Values{"fields": {"modifiedTime,mimeType"}, "key": {c.apiKey}, "supportsAllDrives": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+url.PathEscape(fileID)+"?"+q.Encode(), nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("building drive metadata request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return time.Time{}, fmt.Errorf("asking drive about the video: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, driveStatusError(resp.StatusCode)
	}
	var meta struct {
		ModifiedTime time.Time `json:"modifiedTime"`
		MimeType     string    `json:"mimeType"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return time.Time{}, fmt.Errorf("reading drive metadata: %w", err)
	}
	if !strings.HasPrefix(meta.MimeType, "video/") {
		return time.Time{}, fmt.Errorf("that Drive file is a %s, not a video", meta.MimeType)
	}
	return meta.ModifiedTime, nil
}

// download fetches the bytes into the cache directory under a name made from
// their hash, so a new version never overwrites a file a screen is reading.
func (c *explainerCache) download(ctx context.Context, fileID string) (explainerFile, error) {
	src := c.publicBase + "?" + url.Values{"id": {fileID}, "export": {"download"}, "confirm": {"t"}}.Encode()
	if c.apiKey != "" {
		src = c.apiBase + url.PathEscape(fileID) + "?" + url.Values{"alt": {"media"}, "key": {c.apiKey}, "supportsAllDrives": {"true"}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return explainerFile{}, fmt.Errorf("building drive download request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return explainerFile{}, fmt.Errorf("downloading the video from drive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return explainerFile{}, driveStatusError(resp.StatusCode)
	}
	// A page instead of bytes is Drive's answer to a file that is not shared
	// publicly (a sign-in page) or is too big to scan.
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/html" {
		return explainerFile{}, errors.New(`drive sent a web page, not the video; share the file as "Anyone with the link"`)
	}
	return c.store(fileID, resp.Body)
}

// store writes the body to a temp file, hashes it on the way, and renames it
// into place.
func (c *explainerCache) store(fileID string, body io.Reader) (explainerFile, error) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return explainerFile{}, fmt.Errorf("making the video cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(c.dir, fileID+"-*.part")
	if err != nil {
		return explainerFile{}, fmt.Errorf("creating the video cache file: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once it has been renamed
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, explainerMaxBytes+1))
	closeErr := tmp.Close()
	if err != nil {
		return explainerFile{}, fmt.Errorf("saving the video: %w", err)
	}
	if closeErr != nil {
		return explainerFile{}, fmt.Errorf("saving the video: %w", closeErr)
	}
	if n > explainerMaxBytes {
		return explainerFile{}, fmt.Errorf("the video is over %dMB; export a smaller copy", explainerMaxBytes>>20)
	}
	if n == 0 {
		return explainerFile{}, errors.New("drive sent an empty file")
	}
	sum := hex.EncodeToString(h.Sum(nil))
	dest := filepath.Join(c.dir, fileID+"-"+sum[:12]+".mp4")
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return explainerFile{}, fmt.Errorf("saving the video: %w", err)
	}
	return explainerFile{path: dest, sum: sum, size: n}, nil
}

// driveStatusError turns Drive's status into something an admin can act on.
// Errors here are lower-case like any Go error; explainerMessage capitalises
// them for the settings page.
func driveStatusError(code int) error {
	switch code {
	case http.StatusNotFound:
		return errors.New("drive can't find that file; check the link")
	case http.StatusForbidden, http.StatusUnauthorized:
		return errors.New(`drive won't share that file; set it to "Anyone with the link"`)
	default:
		return fmt.Errorf("drive answered %d", code)
	}
}
