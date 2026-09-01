package trivia

import (
	"bufio"
	"io"
)

// peeker is the three-byte lookahead stripBOM needs. bufio.Reader already
// does this; wrapping it keeps the BOM check readable at the call site.
type peeker struct{ br *bufio.Reader }

func newPeeker(r io.Reader) *peeker { return &peeker{br: bufio.NewReader(r)} }

func (p *peeker) peekBOM() bool {
	b, err := p.br.Peek(3)
	return err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF
}

func (p *peeker) rest(discard int) io.Reader {
	if discard > 0 {
		_, _ = p.br.Discard(discard)
	}
	return p.br
}
