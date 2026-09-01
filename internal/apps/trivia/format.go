package trivia

import (
	"strconv"
	"strings"
)

// FormatValue renders a numeric answer the way a person wrote it: no
// exponent, no trailing zeros, thousands separated.
//
// The TV shows these at 120px and the room reads them from thirty feet, so
// "1.2e+03" would be a genuine defect rather than a cosmetic one.
func FormatValue(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// FormatMoney renders a score or a chip. Scores are whole dollars by
// construction -- cell values and token values are ints -- so this never has
// to decide about cents.
func FormatMoney(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteByte('$')
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
