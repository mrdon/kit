package anthropic

import (
	"encoding/json"
	"strings"
	"testing"
)

// A thinking block must survive decode and re-encode untouched: the API
// validates the signature against the text when the block comes back.
func TestThinkingBlocksRoundTrip(t *testing.T) {
	in := `[{"type":"thinking","thinking":"the user wants a photo","signature":"abc123"},` +
		`{"type":"redacted_thinking","data":"opaque"},` +
		`{"type":"text","text":"hi"}]`
	var blocks []Content
	if err := json.Unmarshal([]byte(in), &blocks); err != nil {
		t.Fatal(err)
	}
	if !blocks[0].IsThinking() || blocks[0].Thinking != "the user wants a photo" || blocks[0].Signature != "abc123" {
		t.Fatalf("thinking block lost fields: %+v", blocks[0])
	}
	if !blocks[1].IsThinking() || blocks[1].Data != "opaque" {
		t.Fatalf("redacted block lost data: %+v", blocks[1])
	}
	out, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"thinking":"the user wants a photo"`, `"signature":"abc123"`, `"data":"opaque"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("re-encoded blocks miss %s: %s", want, out)
		}
	}
}
