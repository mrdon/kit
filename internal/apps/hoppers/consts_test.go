package hoppers

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	consoleassets "github.com/mrdon/kit/web/console"
)

func strs[T ~string](xs ...T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}

// consts.js is what the TV and the phone compare against instead of string
// literals, so it must say exactly what the engine's typed constants say.
func TestConstsMatchEngine(t *testing.T) {
	data, err := consoleassets.HoppersShared("consts.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	groups := map[string][]string{
		"phase":  strs(engine.PhaseLobby, engine.PhaseRacing, engine.PhaseFinal),
		"sub":    strs(engine.SubReady, engine.SubFlick, engine.SubHop, engine.SubRaceEnd),
		"cmd":    strs(engine.CommandTypes...),
		"heat":   strs(engine.HeatWaiting, engine.HeatNext, engine.HeatRacing, engine.HeatDone),
		"result": strs(engine.ResultPlace),
		"spot":   strs(engine.SpotLaunch, engine.SpotChute),
		"layout": strs(engine.Layouts...),
		"msg":    strs(engine.MsgState),
		"role":   strs(engine.Roles...),
	}
	for g, vals := range groups {
		m := regexp.MustCompile(g + `: Object\.freeze\(\{([^}]*)\}\)`).FindStringSubmatch(js)
		if m == nil {
			t.Errorf("consts.js has no %s group", g)
			continue
		}
		got := regexp.MustCompile(`(\w+): "(\w+)"`).FindAllStringSubmatch(m[1], -1)
		if len(got) != len(vals) {
			t.Errorf("%s: consts.js has %d values, the engine has %d %v", g, len(got), len(vals), vals)
		}
		for _, v := range vals {
			if !regexp.MustCompile(fmt.Sprintf(`\b%s: "%s"`, v, v)).MatchString(m[1]) {
				t.Errorf("%s: consts.js is missing %s: %q", g, v, v)
			}
		}
	}
	if !regexp.MustCompile(fmt.Sprintf(`flickBand: %g\b`, engine.BandWidth)).MatchString(js) {
		t.Errorf("consts.js flickBand should be %g", engine.BandWidth)
	}
}
