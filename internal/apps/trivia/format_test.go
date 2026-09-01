package trivia

import "testing"

// These render at 120px on a wall. An exponent or a trailing ".0000001"
// would be read out loud by twenty people at once.
func TestFormatValue(t *testing.T) {
	cases := map[float64]string{
		1969:    "1,969",
		0:       "0",
		-40:     "-40",
		1000000: "1,000,000",
		1.5:     "1.5",
		1200.25: "1,200.25",
		1e3:     "1,000",
		-1234.5: "-1,234.5",
	}
	for in, want := range cases {
		if got := FormatValue(in); got != want {
			t.Errorf("FormatValue(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[int]string{
		0: "$0", 500: "$500", 1000: "$1,000", 16000: "$16,000", -200: "-$200",
	}
	for in, want := range cases {
		if got := FormatMoney(in); got != want {
			t.Errorf("FormatMoney(%d) = %q, want %q", in, got, want)
		}
	}
}
