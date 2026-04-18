// Package sse is a minimal Server-Sent Events writer for HTTP handlers.
//
// It's used by the card chat endpoints today and is shaped so a future
// user-scoped ambient-updates channel can reuse it unchanged.
package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// EventType names a single kind of SSE event. Define typed constants per
// domain (e.g. chat's partial/final/etc.) so stray string literals are a
// compile error at the call site.
type EventType string

// keepAliveInterval is how often the writer emits an SSE comment line to
// keep idle proxies from closing the connection.
const keepAliveInterval = 15 * time.Second

// Writer emits framed SSE events to an HTTP response. Construct with New,
// call Emit for each event, call Close when done. Safe for use from a
// single goroutine (Emit serializes with the keep-alive ticker).
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context

	mu     sync.Mutex
	closed bool
	stopKA chan struct{}
}

// New prepares w for SSE and starts a keep-alive ticker. The caller owns
// request cancellation via ctx; when ctx is done the keep-alive stops and
// subsequent Emit calls return ctx.Err().
//
// Returns an error if w does not implement http.Flusher (required for SSE
// to stream instead of buffering).
func New(w http.ResponseWriter, r *http.Request) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("response writer does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable nginx/Dokku buffering for proxied SSE.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sw := &Writer{
		w:       w,
		flusher: flusher,
		ctx:     r.Context(),
		stopKA:  make(chan struct{}),
	}
	go sw.keepAlive()
	return sw, nil
}

// Emit writes a single SSE event. data is JSON-encoded. Returns the
// request context's error once the client disconnects; returns a separate
// error if the encoded frame could not be written.
func (s *Writer) Emit(event EventType, data any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding sse data: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("sse writer closed")
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return fmt.Errorf("writing sse frame: %w", err)
	}
	s.flusher.Flush()
	return nil
}

// Close stops the keep-alive ticker. The underlying response writer is
// owned by the HTTP server — this does not close it.
func (s *Writer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.stopKA)
}

func (s *Writer) keepAlive() {
	t := time.NewTicker(keepAliveInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopKA:
			return
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			// SSE comment lines start with ":" and are ignored by clients.
			_, _ = fmt.Fprint(s.w, ": keep-alive\n\n")
			s.flusher.Flush()
			s.mu.Unlock()
		}
	}
}
