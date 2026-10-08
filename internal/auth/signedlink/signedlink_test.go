package signedlink

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newSigner(t *testing.T, purpose string) *Signer {
	t.Helper()
	s, err := New("a-test-secret-that-is-long-enough", purpose)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	s := newSigner(t, "test-v1")
	wire := s.Sign([]byte(`{"hello":"world"}`), time.Minute)
	tok, err := s.Verify(wire)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(tok.Payload) != `{"hello":"world"}` {
		t.Fatalf("payload = %q", tok.Payload)
	}
	if tok.ExpiresAt.IsZero() {
		t.Fatal("expected an expiry")
	}
}

func TestNoTTLNeverExpires(t *testing.T) {
	s := newSigner(t, "test-v1")
	wire := s.Sign([]byte("raw"), 0)
	s.Now = func() time.Time { return time.Now().Add(100 * 365 * 24 * time.Hour) }
	tok, err := s.Verify(wire)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !tok.ExpiresAt.IsZero() {
		t.Fatal("expected no expiry")
	}
}

func TestNegativeTTLIsAlreadyExpired(t *testing.T) {
	s := newSigner(t, "test-v1")
	if _, err := s.Verify(s.Sign([]byte("p"), -time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestExpiredIsStillDecodable(t *testing.T) {
	s := newSigner(t, "test-v1")
	base := time.Unix(1_700_000_000, 0)
	s.Now = func() time.Time { return base }
	wire := s.Sign([]byte("p"), time.Second)
	s.Now = func() time.Time { return base.Add(2 * time.Second) }
	if _, err := s.Verify(wire); !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify err = %v, want ErrExpired", err)
	}
	tok, err := s.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(tok.Payload) != "p" || !tok.Expired(s.Now()) {
		t.Fatalf("decoded %+v", tok)
	}
}

func TestTamperRejected(t *testing.T) {
	s := newSigner(t, "test-v1")
	wire := s.Sign([]byte("payload"), time.Minute)
	body, mac, _ := strings.Cut(wire, ".")

	flip := func(in string) string {
		b := []byte(in)
		if b[len(b)-1] == 'A' {
			b[len(b)-1] = 'B'
		} else {
			b[len(b)-1] = 'A'
		}
		return string(b)
	}
	for _, c := range []string{flip(body) + "." + mac, body + "." + flip(mac)} {
		if _, err := s.Verify(c); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("%q: err = %v, want ErrBadSignature", c, err)
		}
	}
}

func TestMalformedRejected(t *testing.T) {
	s := newSigner(t, "test-v1")
	for _, c := range []string{"", "nodot", ".", "AAAA.", "!!!.!!!", "AA.AA"} {
		if _, err := s.Verify(c); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%q: err = %v, want ErrMalformed", c, err)
		}
	}
}

func TestPurposeSeparation(t *testing.T) {
	a := newSigner(t, "purpose-a")
	b := newSigner(t, "purpose-b")
	if _, err := b.Verify(a.Sign([]byte("x"), 0)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("cross-purpose verify err = %v, want ErrBadSignature", err)
	}
}

func TestEmptySecret(t *testing.T) {
	for _, secret := range []string{"", "   "} {
		if _, err := New(secret, "p"); !errors.Is(err, ErrNoSecret) {
			t.Fatalf("New(%q) err = %v, want ErrNoSecret", secret, err)
		}
	}
}
