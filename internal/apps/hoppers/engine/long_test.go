package engine

import "os"

// longTests turns on the exhaustive versions of the slow engine checks
// (every golden night under every option, every save point on two seeds).
// They take tens of seconds under -race, so CI and prepush run a quick
// sample instead; run the full sweep after changing the rules with
//
//	make hoppers-long-test
var longTests = os.Getenv("HOPPERS_LONG_TESTS") != ""
