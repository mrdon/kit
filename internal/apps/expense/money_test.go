package expense

import "testing"

func TestParseCents(t *testing.T) {
	ok := map[string]int64{
		"12.34":    1234,
		"$12.34":   1234,
		"12":       1200,
		"12.5":     1250,
		"0.09":     9,
		"1,234.56": 123456,
		"  42.00 ": 4200,
		"-5.00":    -500,
		"£3.40":    340,
	}
	for in, want := range ok {
		got, err := parseCents(in)
		if err != nil {
			t.Errorf("parseCents(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseCents(%q) = %d, want %d", in, got, want)
		}
	}
	bad := []string{"", "abc", "1.234", "12.3.4", "$"}
	for _, in := range bad {
		if _, err := parseCents(in); err == nil {
			t.Errorf("parseCents(%q) expected error, got nil", in)
		}
	}
}

func TestFormatCents(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{1234, "USD", "$12.34"},
		{1234, "EUR", "€12.34"},
		{1234, "GBP", "£12.34"},
		{1234, "JPY", "12.34 JPY"},
		{900, "USD", "$9.00"},
		{0, "", "$0.00"},
	}
	for _, c := range cases {
		if got := formatCents(c.cents, c.currency); got != c.want {
			t.Errorf("formatCents(%d, %q) = %q, want %q", c.cents, c.currency, got, c.want)
		}
	}
}
