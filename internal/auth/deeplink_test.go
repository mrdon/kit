package auth

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestSigner(t *testing.T) *DeepLinkSigner {
	t.Helper()
	s, err := NewDeepLinkSigner("test-secret-please-ignore")
	if err != nil {
		t.Fatalf("NewDeepLinkSigner: %v", err)
	}
	return s
}

func TestDeepLinkSignerRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	user, tenant, entry := uuid.New(), uuid.New(), uuid.New()

	tok, err := s.Sign(user, tenant, entry, 2*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID != user || claims.TenantID != tenant || claims.EntryID != entry {
		t.Fatalf("claims mismatch: got %+v", claims)
	}
	if claims.JTI == "" {
		t.Fatalf("jti not populated")
	}
}

func TestDeepLinkSignerRejectsTamper(t *testing.T) {
	s := newTestSigner(t)
	tok, err := s.Sign(uuid.New(), uuid.New(), uuid.New(), 2*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Flip a character in the signature segment.
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("token shape unexpected")
	}
	bad := flipFirstChar(parts[1])
	tampered := parts[0] + "." + bad

	_, err = s.Verify(tampered)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != DeepLinkBadSig {
		t.Fatalf("want bad_sig, got %v", err)
	}
}

func TestDeepLinkSignerRejectsExpired(t *testing.T) {
	s := newTestSigner(t)
	// Pin signing time so we control expiry deterministically.
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }

	tok, err := s.Sign(uuid.New(), uuid.New(), uuid.New(), 1*time.Second)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verify at +2s — past the 1s TTL.
	s.now = func() time.Time { return now.Add(2 * time.Second) }

	_, err = s.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != DeepLinkExpired {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestDeepLinkSignerRejectsMalformed(t *testing.T) {
	s := newTestSigner(t)
	cases := []string{
		"",
		"no-dot",
		"only.one.section.too.many",
		"!notbase64.alsonotbase64",
	}
	for _, c := range cases {
		_, err := s.Verify(c)
		var ve *VerifyError
		if !errors.As(err, &ve) {
			t.Fatalf("input %q: want VerifyError, got %v", c, err)
		}
	}
}

func TestDeepLinkConsumeJTIOnce(t *testing.T) {
	s := newTestSigner(t)
	tok, err := s.Sign(uuid.New(), uuid.New(), uuid.New(), 2*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := s.ConsumeJTI(claims.JTI); err != nil {
		t.Fatalf("first ConsumeJTI: %v", err)
	}
	err = s.ConsumeJTI(claims.JTI)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != DeepLinkConsumed {
		t.Fatalf("second ConsumeJTI: want consumed, got %v", err)
	}
}

// TestDeepLinkConsumeJTIRace exercises the mutex under concurrent
// consume of the same jti. Exactly one goroutine should win.
func TestDeepLinkConsumeJTIRace(t *testing.T) {
	s := newTestSigner(t)
	tok, err := s.Sign(uuid.New(), uuid.New(), uuid.New(), 2*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	const goroutines = 32
	var (
		wg      sync.WaitGroup
		ready   sync.WaitGroup
		start   = make(chan struct{})
		winners atomic.Int32
	)
	ready.Add(goroutines)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			if err := s.ConsumeJTI(claims.JTI); err == nil {
				winners.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("want exactly 1 winner, got %d", got)
	}
}

func TestDeepLinkSignerRejectsWrongKey(t *testing.T) {
	a := newTestSigner(t)
	b, err := NewDeepLinkSigner("different-secret")
	if err != nil {
		t.Fatalf("NewDeepLinkSigner: %v", err)
	}
	tok, err := a.Sign(uuid.New(), uuid.New(), uuid.New(), 2*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = b.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != DeepLinkBadSig {
		t.Fatalf("want bad_sig under different key, got %v", err)
	}
}

func TestNewDeepLinkSignerEmptySecret(t *testing.T) {
	if _, err := NewDeepLinkSigner(""); !errors.Is(err, ErrSessionMisconfigured) {
		t.Fatalf("want ErrSessionMisconfigured, got %v", err)
	}
	if _, err := NewDeepLinkSigner("   "); !errors.Is(err, ErrSessionMisconfigured) {
		t.Fatalf("want ErrSessionMisconfigured on whitespace, got %v", err)
	}
}

// flipFirstChar replaces the first character with a distinct one so the
// HMAC verify must fail without changing the segment's base64-ness.
func flipFirstChar(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}
