package console

import (
	"testing"

	"github.com/mrdon/kit/internal/services"
)

// A device's installed app must start at /pair, the one page that works
// whether or not the home-screen copy kept its pairing. A person's starts on
// the console.
func TestManifestURLStartsDevicesAtPair(t *testing.T) {
	cases := []struct {
		name   string
		caller *services.Caller
		want   string
	}{
		{"user", &services.Caller{Kind: services.CallerUser}, "/gravity/web/manifest.webmanifest"},
		{"zero kind is a user", &services.Caller{}, "/gravity/web/manifest.webmanifest"},
		{"device", &services.Caller{Kind: services.CallerDevice}, "/gravity/web/manifest.webmanifest?start=pair"},
		{"no caller", nil, "/gravity/web/manifest.webmanifest"},
	}
	for _, c := range cases {
		if got := manifestURL("gravity", c.caller); got != c.want {
			t.Errorf("%s: manifestURL = %q, want %q", c.name, got, c.want)
		}
	}
}
