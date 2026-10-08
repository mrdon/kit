package opaquetoken

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewIsPrefixedRandomAndHashed(t *testing.T) {
	tok, hash, err := New("kit_")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.HasPrefix(tok, "kit_") || len(tok) != 4+64 {
		t.Fatalf("token = %q", tok)
	}
	if hash != Hash(tok) || hash == tok {
		t.Fatalf("hash = %q for token %q", hash, tok)
	}
	if hex.EncodeToString(Sum(tok)) != hash {
		t.Fatal("Sum and Hash disagree")
	}
	other, _, _ := New("kit_")
	if other == tok {
		t.Fatal("two tokens collided")
	}
}
