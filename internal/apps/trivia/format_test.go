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

// A pack's answer column and a phone's guess both arrive as typed; the wall
// adds the commas the typist left out and keeps everything else they wrote.
func TestDisplayAnswer(t *testing.T) {
	cases := []struct {
		raw  string
		v    float64
		want string
	}{
		{"867530", 867530, "867,530"},
		{"$1200", 1200, "$1,200"},
		{"45000%", 45000, "45,000%"},
		{"1,200", 1200, "1,200"},
		{"1200.5", 1200.5, "1,200.5"},
		{"-12000", -12000, "-12,000"},
		{"999", 999, "999"},
		{"", 3000000, "3,000,000"},
		{"1 200", 1200, "1 200"},
	}
	for _, c := range cases {
		if got := DisplayAnswer(c.raw, c.v); got != c.want {
			t.Errorf("DisplayAnswer(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
