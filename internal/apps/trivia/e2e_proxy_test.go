//go:build e2e

package trivia

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// faultMode is what the bar's wifi is doing right now.
type faultMode int

const (
	netOK faultMode = iota
	// netStall: sockets stay open and look healthy, but no byte moves in
	// either direction -- the classic zombie wifi association. New
	// connections are accepted and stall too. Bytes resume, in order, when
	// the mode clears, as TCP retransmission would deliver them.
	netStall
	// netDown: every live connection is reset and new ones are refused.
	netDown
)

// chaosProxy sits between the browsers and the Kit server and misbehaves on
// command. Its backend can be repointed, which is how the harness simulates a
// deploy: the old server dies, a new one takes over the same address.
type chaosProxy struct {
	ln      net.Listener
	mu      sync.Mutex
	cond    *sync.Cond
	mode    faultMode
	latency time.Duration
	backend string
	pairs   map[*pair]struct{}
}

// pair is one proxied connection. A zombie pair never moves another byte:
// the socket stays open on both ends and everything sent into it vanishes,
// which is what a phone sees after its wifi silently re-associates and the
// old NAT entry is gone. New connections are unaffected.
type pair struct {
	client, server net.Conn
	zombie         bool
	closed         bool // set on teardown so a held chunk stops waiting
}

func newChaosProxy(t *testing.T, backend string) *chaosProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &chaosProxy{ln: ln, backend: backend, pairs: map[*pair]struct{}{}}
	p.cond = sync.NewCond(&p.mu)
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.set(netDown) })
	return p
}

func (p *chaosProxy) URL() string { return "http://" + p.ln.Addr().String() }

func (p *chaosProxy) setBackend(addr string) {
	p.mu.Lock()
	p.backend = addr
	p.mu.Unlock()
}

func (p *chaosProxy) setLatency(d time.Duration) {
	p.mu.Lock()
	p.latency = d
	p.mu.Unlock()
}

// set changes the mode. netDown resets everything that is open right now.
func (p *chaosProxy) set(m faultMode) {
	p.mu.Lock()
	p.mode = m
	var kill []net.Conn
	if m == netDown {
		for pr := range p.pairs {
			kill = append(kill, pr.client, pr.server)
		}
	}
	p.cond.Broadcast()
	p.mu.Unlock()
	for _, c := range kill {
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0) // RST, not a polite FIN
		}
		_ = c.Close()
	}
}

// zombify turns every connection open right now into a zombie.
func (p *chaosProxy) zombify() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for pr := range p.pairs {
		pr.zombie = true
	}
	return len(p.pairs)
}

func (p *chaosProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(client)
	}
}

func (p *chaosProxy) handle(client net.Conn) {
	p.mu.Lock()
	mode, backend := p.mode, p.backend
	p.mu.Unlock()
	if mode == netDown {
		_ = client.Close()
		return
	}
	server, err := net.Dial("tcp", backend)
	if err != nil {
		_ = client.Close() // the server is mid-deploy: connection refused
		return
	}
	pr := &pair{client: client, server: server}
	p.mu.Lock()
	p.pairs[pr] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pairs, pr)
		pr.closed = true
		p.cond.Broadcast()
		p.mu.Unlock()
		_ = client.Close()
		_ = server.Close()
	}()
	done := make(chan struct{}, 2)
	go func() { p.pipe(server, client, pr); done <- struct{}{} }()
	go func() { p.pipe(client, server, pr); done <- struct{}{} }()
	<-done
}

// pipe copies src to dst one read at a time, holding each chunk while the
// network is stalled and delaying it by the current latency.
func (p *chaosProxy) pipe(dst io.Writer, src io.Reader, pr *pair) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if !p.gate(pr) {
				return
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// gate blocks while stalled (or for good, on a zombie) and applies latency.
// False means the connection died while the chunk was held.
func (p *chaosProxy) gate(pr *pair) bool {
	p.mu.Lock()
	for !pr.closed && p.mode != netDown && (p.mode == netStall || pr.zombie) {
		p.cond.Wait()
	}
	down, lat := p.mode == netDown || pr.closed, p.latency
	p.mu.Unlock()
	if down {
		return false
	}
	if lat > 0 {
		time.Sleep(lat)
	}
	return true
}
