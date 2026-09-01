package sse

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The reason commit 0 of the trivia build exists: the HTTP server carries a
// WriteTimeout as its slowloris guard, and that deadline is absolute from the
// moment the request headers are read. Before New cleared it, an SSE stream
// died at WriteTimeout no matter how recently it had written -- truncating
// card chat on long agent turns today, and making a game that runs for an
// hour impossible.
//
// A 2s server timeout stands in for production's 30s so the test is fast; the
// mechanism is identical, and the assertion is that a frame written after the
// deadline has passed still reaches the client.
func TestStreamOutlivesServerWriteTimeout(t *testing.T) {
	const writeTimeout = 500 * time.Millisecond

	done := make(chan error, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw, err := New(w, r)
		if err != nil {
			done <- err
			return
		}
		defer sw.Close()
		if err := sw.Emit("tick", map[string]int{"n": 1}); err != nil {
			done <- err
			return
		}
		// Cross the server's write deadline with the stream idle, then write
		// again. This is the write that used to fail.
		time.Sleep(3 * writeTimeout)
		done <- sw.Emit("tick", map[string]int{"n": 2})
	}))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var got []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() && len(got) < 2 {
		if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
			got = append(got, strings.TrimPrefix(line, "data: "))
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("handler: %v", err)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("received %d frames %v, want 2 -- the post-deadline frame was dropped", len(got), got)
	}
	if got[1] != `{"n":2}` {
		t.Fatalf("second frame = %s", got[1])
	}
}
