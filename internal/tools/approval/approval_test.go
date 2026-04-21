package approval_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/tools/approval"
)

func TestFromCtxUnpopulated(t *testing.T) {
	_, _, ok := approval.FromCtx(context.Background())
	if ok {
		t.Fatalf("expected ok=false from bare ctx")
	}
}

func TestWithTokenRoundTrip(t *testing.T) {
	card := uuid.New()
	tok := uuid.New()
	ctx := approval.WithToken(context.Background(), approval.Mint(card, tok))
	gotCard, gotTok, ok := approval.FromCtx(ctx)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if gotCard != card {
		t.Fatalf("card id roundtrip: got %s want %s", gotCard, card)
	}
	if gotTok != tok {
		t.Fatalf("resolve token roundtrip: got %s want %s", gotTok, tok)
	}
}

func TestZeroTokenRejected(t *testing.T) {
	// Explicitly attaching a zero token must NOT count as approval —
	// otherwise ctx.WithValue(ctxKey{}, Token{}) would bypass the gate.
	ctx := approval.WithToken(context.Background(), approval.Token{})
	_, _, ok := approval.FromCtx(ctx)
	if ok {
		t.Fatalf("zero token should not authorise; got ok=true")
	}
}

func TestTokenAccessors(t *testing.T) {
	card := uuid.New()
	tok := uuid.New()
	t0 := approval.Mint(card, tok)
	if t0.CardID() != card {
		t.Errorf("CardID mismatch")
	}
	if t0.ResolveToken() != tok {
		t.Errorf("ResolveToken mismatch")
	}
	var zero approval.Token
	if zero.CardID() != uuid.Nil {
		t.Errorf("zero Token CardID should be Nil")
	}
}
