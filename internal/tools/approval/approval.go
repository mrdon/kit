// Package approval carries the approval-token ceremony that allows a
// PolicyGate tool call to execute. The token binds a specific decision
// card's id and a resolve idempotency token to an approved context.
//
// Security model: only code holding a token minted by Mint AND attaching
// it to a Context via WithToken can cause Registry.Execute to dispatch a
// PolicyGate tool's handler. Token's fields are unexported so values
// cannot be forged outside this package; grep for approval.WithToken
// enumerates every gate-bypass call site in the repo. The ONLY
// legitimate caller is CardService.ResolveDecision after verifying the
// user has approved the card through the normal swipe/card UI.
//
// The plan originally specified an unexported field on tools.ExecContext
// (see /home/mrdon/.claude/plans/look-at-the-decision-synchronous-comet.md
// §11). We use a context.Value instead because Go's unexported fields
// cannot be written from a subpackage; the security property survives
// because Token's fields are unexported and the ctx-key is unexported.
package approval

import (
	"context"

	"github.com/google/uuid"
)

// Token binds a specific card id + resolve token to an approved
// execution. Construct via Mint; attach to a Context via WithToken.
// A zero Token has cardID == uuid.Nil and is rejected by FromCtx.
type Token struct {
	cardID       uuid.UUID
	resolveToken uuid.UUID
}

// Mint builds a Token for the given card and resolve-idempotency
// token. Callable from anywhere, but a Token value by itself does
// nothing until placed into a Context via WithToken.
func Mint(cardID, resolveToken uuid.UUID) Token {
	return Token{cardID: cardID, resolveToken: resolveToken}
}

// ctxKey is unexported so no external package can plant a value under
// this key except by calling WithToken.
type ctxKey struct{}

// WithToken returns ctx annotated with t as the approval token. This is
// the single canonical way to authorise a PolicyGate tool invocation.
// Audit every caller.
func WithToken(ctx context.Context, t Token) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromCtx returns the card id and resolve token bound to ctx, or
// (uuid.Nil, uuid.Nil, false) if ctx carries no approval.
func FromCtx(ctx context.Context) (cardID, resolveToken uuid.UUID, ok bool) {
	t, _ := ctx.Value(ctxKey{}).(Token)
	if t.cardID == uuid.Nil {
		return uuid.Nil, uuid.Nil, false
	}
	return t.cardID, t.resolveToken, true
}

// CardID returns the card the approval refers to. Zero Token returns
// uuid.Nil.
func (t Token) CardID() uuid.UUID { return t.cardID }

// ResolveToken returns the idempotency key. Zero Token returns
// uuid.Nil.
func (t Token) ResolveToken() uuid.UUID { return t.resolveToken }
