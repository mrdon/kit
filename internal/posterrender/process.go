package posterrender

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Config says where the renderer is, or how to start one.
type Config struct {
	// URL of a renderer already running somewhere. When set, no child
	// process is started and Token authenticates to it.
	URL   string
	Token string
	// Dir holds the built renderer (dist/server.js). Used only when URL is
	// empty. Node is the node binary; empty means "node" on PATH.
	Dir  string
	Node string
	// Port the child listens on (localhost only).
	Port int
	// CacheDir is where the child keeps prepared images and fonts.
	CacheDir string
	// DriveAPIKey lets the child download Drive files through the API
	// rather than the public download endpoint.
	DriveAPIKey string
}

// Supervisor keeps a renderer child alive: starts it on demand, restarts
// it after a crash with a short backoff, and stops it at shutdown. The
// Client it exposes points at the child.
type Supervisor struct {
	cfg     Config
	client  *Client
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stop    chan struct{}
	stopped bool
}

// Start returns a Renderer for cfg. With a URL it is a plain client; with a
// Dir it starts the child and waits briefly for it to answer.
func Start(ctx context.Context, cfg Config) (Renderer, func(), error) {
	if cfg.URL != "" {
		c := NewClient(cfg.URL, cfg.Token)
		if !c.Healthy(ctx) {
			slog.Warn("poster renderer not answering yet", "url", cfg.URL)
		}
		return c, func() {}, nil
	}
	if cfg.Dir == "" {
		return nil, nil, errors.New("no renderer: set POSTER_RENDERER_URL or POSTER_RENDERER_DIR")
	}
	entry := filepath.Join(cfg.Dir, "dist", "server.js")
	if _, err := os.Stat(entry); err != nil {
		return nil, nil, fmt.Errorf("renderer not built at %s (run make renderer-build): %w", entry, err)
	}
	if cfg.Port == 0 {
		cfg.Port = 8491
	}
	if cfg.Node == "" {
		cfg.Node = "node"
	}
	s := &Supervisor{cfg: cfg, client: NewClient(fmt.Sprintf("http://127.0.0.1:%d", cfg.Port), ""), stop: make(chan struct{})}
	if err := s.launch(); err != nil {
		return nil, nil, err
	}
	go s.loop()
	s.awaitHealthy(ctx, 15*time.Second)
	return s.client, s.Stop, nil
}

func (s *Supervisor) launch() error {
	entry := filepath.Join(s.cfg.Dir, "dist", "server.js")
	cmd := exec.Command(s.cfg.Node, entry)
	cmd.Dir = s.cfg.Dir
	cmd.Env = append(os.Environ(),
		"POSTER_RENDERER_PORT="+strconv.Itoa(s.cfg.Port),
		"POSTER_RENDERER_HOST=127.0.0.1",
		"POSTER_RENDERER_TOKEN=",
	)
	if s.cfg.CacheDir != "" {
		cmd.Env = append(cmd.Env, "POSTER_RENDERER_CACHE_DIR="+s.cfg.CacheDir)
	}
	if s.cfg.DriveAPIKey != "" {
		cmd.Env = append(cmd.Env, "GDRIVE_API_KEY="+s.cfg.DriveAPIKey)
	}
	// The child watches its stdin: if Kit dies without a chance to signal
	// it, the pipe closes and it exits rather than lingering on the port.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("renderer stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("renderer stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("renderer stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting renderer (%s %s): %w", s.cfg.Node, entry, err)
	}
	go relay(stdout, slog.LevelInfo)
	go relay(stderr, slog.LevelWarn)
	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.mu.Unlock()
	slog.Info("poster renderer started", "pid", cmd.Process.Pid, "port", s.cfg.Port)
	return nil
}

func relay(r io.Reader, level slog.Level) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		slog.Log(context.Background(), level, sc.Text(), "source", "poster-renderer")
	}
}

// loop waits on the child and restarts it until Stop is called.
func (s *Supervisor) loop() {
	backoff := time.Second
	for {
		s.mu.Lock()
		cmd := s.cmd
		s.mu.Unlock()
		err := cmd.Wait()
		select {
		case <-s.stop:
			return
		default:
		}
		slog.Warn("poster renderer exited; restarting", "error", err, "backoff", backoff)
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
		if err := s.launch(); err != nil {
			slog.Error("poster renderer restart failed", "error", err)
			time.Sleep(backoff)
			continue
		}
		if s.client.Healthy(context.Background()) {
			backoff = time.Second
		}
	}
}

func (s *Supervisor) awaitHealthy(ctx context.Context, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if s.client.Healthy(ctx) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	slog.Warn("poster renderer did not answer in time; requests will retry against it", "port", s.cfg.Port)
}

// Stop ends the child. Safe to call more than once.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stop)
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_, _ = s.cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
		}
	}
}
