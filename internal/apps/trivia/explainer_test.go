package trivia

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testDriveID = "1AbCdEfGhIjKlMnOpQrStUvWxYz012345"

func TestDriveFileID(t *testing.T) {
	ok := map[string]string{
		testDriveID: testDriveID,
		"https://drive.google.com/file/d/" + testDriveID + "/view?usp=sharing": testDriveID,
		"  https://drive.google.com/file/d/" + testDriveID + "/view  ":         testDriveID,
		"https://drive.google.com/open?id=" + testDriveID:                      testDriveID,
		"https://drive.google.com/uc?export=download&id=" + testDriveID:        testDriveID,
		"https://docs.google.com/file/d/" + testDriveID + "/edit":              testDriveID,
	}
	for in, want := range ok {
		got, err := driveFileID(in)
		if err != nil || got != want {
			t.Errorf("driveFileID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"not a link",
		"https://example.com/file/d/" + testDriveID + "/view",
		"https://drive.google.com/drive/folders/" + testDriveID,
		"https://drive.google.com/file/d/short/view",
	}
	for _, in := range bad {
		if got, err := driveFileID(in); err == nil {
			t.Errorf("driveFileID(%q) = %q, want an error", in, got)
		}
	}
}

// fakeDrive serves a file the way Drive does, and counts what it was asked.
type fakeDrive struct {
	body      atomic.Value // string
	modified  atomic.Value // string, RFC 3339
	status    atomic.Int32
	html      atomic.Bool
	downloads atomic.Int32
	metas     atomic.Int32
}

func newFakeDrive(t *testing.T) (*fakeDrive, *httptest.Server) {
	d := &fakeDrive{}
	d.body.Store("video-bytes-v1")
	d.modified.Store("2026-01-02T03:04:05Z")
	d.status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := int(d.status.Load()); code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		if r.URL.Query().Get("fields") != "" {
			d.metas.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"mimeType":"video/mp4","modifiedTime":"`+d.modified.Load().(string)+`"}`)
			return
		}
		d.downloads.Add(1)
		if d.html.Load() {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<html>sign in</html>")
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(w, d.body.Load().(string))
	}))
	t.Cleanup(srv.Close)
	return d, srv
}

// testCache points a cache at the fake, with a clock the test moves.
func testCache(t *testing.T, srv *httptest.Server, key string) (*explainerCache, *time.Time) {
	c := newExplainerCache(t.TempDir(), key)
	c.apiBase = srv.URL + "/files/"
	c.publicBase = srv.URL + "/download"
	now := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, &now
}

func readCached(t *testing.T, f explainerFile) string {
	t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatalf("reading cached video: %v", err)
	}
	return string(b)
}

func TestExplainerCacheWithoutKeyRefetchesHourly(t *testing.T) {
	d, srv := newFakeDrive(t)
	c, now := testCache(t, srv, "")
	ctx := context.Background()

	first, err := c.Get(ctx, testDriveID)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if readCached(t, first) != "video-bytes-v1" {
		t.Fatalf("cached the wrong bytes")
	}
	if _, err := c.Get(ctx, testDriveID); err != nil || d.downloads.Load() != 1 {
		t.Fatalf("a second get inside the hour downloaded again (%d downloads, err %v)", d.downloads.Load(), err)
	}

	// An hour on with the same bytes: downloaded again, timestamp kept, so
	// screens' copies stay valid.
	*now = now.Add(explainerRecheckNoKey + time.Minute)
	same, err := c.Get(ctx, testDriveID)
	if err != nil || d.downloads.Load() != 2 {
		t.Fatalf("expected a re-download after the hour (%d downloads, err %v)", d.downloads.Load(), err)
	}
	if !same.modified.Equal(first.modified) {
		t.Errorf("unchanged bytes moved Last-Modified from %v to %v", first.modified, same.modified)
	}

	// A new version replaces the copy and moves the timestamp.
	d.body.Store("video-bytes-v2")
	*now = now.Add(explainerRecheckNoKey + time.Minute)
	next, err := c.Get(ctx, testDriveID)
	if err != nil {
		t.Fatalf("get after a new version: %v", err)
	}
	if readCached(t, next) != "video-bytes-v2" || !next.modified.After(first.modified) {
		t.Errorf("new version not picked up: %q at %v", readCached(t, next), next.modified)
	}
	if _, err := os.Stat(first.path); !os.IsNotExist(err) {
		t.Errorf("the old copy was left on disk")
	}
}

func TestExplainerCacheWithKeyChecksModifiedTime(t *testing.T) {
	d, srv := newFakeDrive(t)
	c, now := testCache(t, srv, "test-key")
	ctx := context.Background()

	first, err := c.Get(ctx, testDriveID)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if want, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z"); !first.modified.Equal(want) {
		t.Errorf("Last-Modified = %v, want Drive's modifiedTime %v", first.modified, want)
	}

	*now = now.Add(explainerRecheckWithKey + time.Second)
	if _, err := c.Get(ctx, testDriveID); err != nil || d.downloads.Load() != 1 {
		t.Fatalf("an unchanged file was downloaded again (%d downloads, err %v)", d.downloads.Load(), err)
	}

	d.modified.Store("2026-02-02T03:04:05Z")
	d.body.Store("video-bytes-v2")
	*now = now.Add(explainerRecheckWithKey + time.Second)
	next, err := c.Get(ctx, testDriveID)
	if err != nil || d.downloads.Load() != 2 || readCached(t, next) != "video-bytes-v2" {
		t.Fatalf("a changed file was not re-downloaded (%d downloads, err %v)", d.downloads.Load(), err)
	}
}

func TestExplainerCacheKeepsOldCopyWhenDriveFails(t *testing.T) {
	d, srv := newFakeDrive(t)
	c, now := testCache(t, srv, "")
	ctx := context.Background()
	if _, err := c.Get(ctx, testDriveID); err != nil {
		t.Fatalf("first get: %v", err)
	}
	d.status.Store(http.StatusInternalServerError)
	*now = now.Add(explainerRecheckNoKey + time.Minute)
	got, err := c.Get(ctx, testDriveID)
	if err != nil || readCached(t, got) != "video-bytes-v1" {
		t.Fatalf("a Drive outage took the video away: %v", err)
	}
}

func TestExplainerCacheRefusesWhatIsNotAVideo(t *testing.T) {
	d, srv := newFakeDrive(t)
	c, _ := testCache(t, srv, "")
	d.html.Store(true)
	if _, err := c.Get(context.Background(), testDriveID); err == nil || !strings.Contains(err.Error(), "Anyone with the link") {
		t.Fatalf("a sign-in page was accepted as the video: %v", err)
	}
	d.html.Store(false)
	d.status.Store(http.StatusForbidden)
	if _, err := c.Get(context.Background(), testDriveID); err == nil || !strings.Contains(err.Error(), "Anyone with the link") {
		t.Fatalf("a 403 did not say how to fix the sharing: %v", err)
	}
}

func TestExplainerMessage(t *testing.T) {
	if got := explainerMessage(errString("drive can't find that file; check the link")); got != "Drive can't find that file; check the link." {
		t.Errorf("explainerMessage = %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
