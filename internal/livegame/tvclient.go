package livegame

import _ "embed"

// tvClientJS is KitLive, the TV page's live connection. See tvlive.js.
//
//go:embed tvlive.js
var tvClientJS []byte

// TVClientJS returns the TV live client, to be inlined into a TV page ahead
// of the game's own script. It defines one global, KitLive.
func TVClientJS() []byte { return tvClientJS }
