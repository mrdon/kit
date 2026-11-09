package posters

import "testing"

func TestSkipFolder(t *testing.T) {
	cases := []struct {
		rel, name string
		want      bool
	}{
		{"", "Beers", true},
		{"", "beers ", true},
		{"", "2026-10-28 Beer", false},
		{"", "Light Lift Lager", false},
		{"2026-10-28-beer", "Beers", false},
	}
	for _, c := range cases {
		if got := skipFolder(c.rel, c.name); got != c.want {
			t.Errorf("skipFolder(%q, %q) = %v, want %v", c.rel, c.name, got, c.want)
		}
	}
}
