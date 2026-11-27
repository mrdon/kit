package trivia

import (
	"regexp"
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

// plainNumber matches an answer written as bare digits with an optional
// prefix or suffix ("1000000", "$1200", "45000%") -- the forms worth
// regrouping. Anything with a comma already, or words, is left as written.
var plainNumber = regexp.MustCompile(`^(\D*?)(-?\d+(?:\.\d+)?)(\D*)$`)

// DisplayAnswer renders an answer for a screen: the spelling a human wrote,
// with thousands separators added when they left them out, so "867530" reads
// as "867,530" from across the room. Blank text falls back to the value.
func DisplayAnswer(raw string, v float64) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return FormatValue(v)
	}
	m := plainNumber.FindStringSubmatch(raw)
	if m == nil || strings.Contains(raw, ",") {
		return raw
	}
	n, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return raw
	}
	return m[1] + FormatValue(n) + m[3]
}
