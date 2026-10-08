package trivia

import "testing"

func TestJoinURL(t *testing.T) {
	cases := map[string]string{
		"https://kit.example.com":  "https://kit.example.com/acme/trivia/brave-otter-lamp",
		"https://kit.example.com/": "https://kit.example.com/acme/trivia/brave-otter-lamp",
	}
	for base, want := range cases {
		if got := JoinURL(base, "acme", "brave-otter-lamp"); got != want {
			t.Errorf("JoinURL(%q) = %q, want %q", base, got, want)
		}
	}
}
