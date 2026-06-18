package expense

import (
	"errors"
	"strconv"
	"strings"
)

// errBadAmount is returned when a money string can't be parsed.
var errBadAmount = errors.New("amount must be a number like 12.34")

// parseCents turns a human money string into integer minor units. Accepts an
// optional currency symbol/grouping ("$1,234.56", "12.34", "12", "12.5") and
// rejects anything with more than two fractional digits. The agent passes
// dollars-and-cents; storing cents keeps arithmetic exact.
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errBadAmount
	}
	// Strip a leading currency symbol and any thousands separators.
	s = strings.TrimLeft(s, "$£€ ")
	s = strings.ReplaceAll(s, ",", "")
	neg := false
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		neg, s = true, rest
	}
	if s == "" {
		return 0, errBadAmount // nothing but a symbol/sign
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, errBadAmount
	}
	var f int64
	if hasFrac {
		switch len(frac) {
		case 0:
			f = 0
		case 1:
			f, err = strconv.ParseInt(frac, 10, 64)
			f *= 10
		case 2:
			f, err = strconv.ParseInt(frac, 10, 64)
		default:
			return 0, errBadAmount
		}
		if err != nil {
			return 0, errBadAmount
		}
	}
	cents := w*100 + f
	if neg {
		cents = -cents
	}
	return cents, nil
}
