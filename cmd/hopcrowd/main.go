// Command hopcrowd plays a live Hoppers game in kit like a bar full of
// phones: N fake players join over the same endpoints the phone page uses
// (POST /join with a cookie jar each, the SSE /stream or the /state poll
// fallback, POST /flick), flick on their turns (some slow, some asleep), and
// (with -host) run the night through the console's host API with a signed-in
// session. It logs the announcer feed and heat line-ups so a run can be
// reviewed.
//
// -game is the game's phone URL, as the QR redirects to. -session is the
// value of a signed-in browser's kit_session cookie for that workspace (any
// member, or a device with hoppers.host).
//
//	go run ./cmd/hopcrowd -game https://kit.example/acme/hoppers/jumping-lion -players 4
//	go run ./cmd/hopcrowd -game ... -players 30 -host -session <cookie>  # heats + ladder final
//	... -churn   # drop one phone and reclaim another mid-night (see churn.go)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var names = []string{"Bubbles McGee", "Wartholomew", "Croaky Joe", "Princess Ribbit", "Toad Rage", "Lily Liver", "Hopscotch", "Sir Splash"}
var colors = []string{"#e67e22", "#9b59b6", "#00cec9", "#fd79a8", "#8e5a2b", "#e74c3c", "#636e72", "#1abc9c", "#2ecc71", "#3498db", "#f1c40f", "#e84393"}
var hats = []string{"tophat", "viking", "chef", "party", "antlers", "bucket", "headband", "propeller"}

// styles: how fast a fake person flicks once it's their turn.
var styles = []string{"quick", "normal", "normal", "slow", "asleep"}

type opts struct {
	game, session, logPath          string
	players, pollEvery              int
	host, churn                     bool
	breakFor, resultsFor, churnAway time.Duration
}

func main() {
	var o opts
	flag.StringVar(&o.game, "game", "", "the game's phone URL: https://host/{slug}/hoppers/{game}")
	flag.IntVar(&o.players, "players", 4, "fake phones")
	flag.BoolVar(&o.host, "host", false, "also act as host: start the night and each round after the beer break")
	flag.StringVar(&o.session, "session", "", "a signed-in kit_session cookie value for the workspace (needed with -host)")
	flag.StringVar(&o.logPath, "log", "", "write the announcer feed here")
	flag.DurationVar(&o.breakFor, "break", 10*time.Second, "how long the host sits on a round's last results (the beer break)")
	flag.DurationVar(&o.resultsFor, "results", 7*time.Second, "how long the host shows a heat's results before the next heat")
	flag.IntVar(&o.pollEvery, "poll", 4, "every Nth phone uses the /state poll fallback instead of the stream (0 = none)")
	flag.BoolVar(&o.churn, "churn", false, "once racing, drop phone 1 and lose phone 2, then bring them back (needs -host and 3+ players)")
	flag.DurationVar(&o.churnAway, "churnaway", 25*time.Second, "how long churned phones stay away (past the seen window)")
	flag.Parse()
	if err := run(o); err != nil {
		slog.Error("hopcrowd", "error", err)
		os.Exit(1)
	}
}

func run(o opts) error {
	logLine, closeLog, err := openLog(o.logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	base := strings.TrimRight(o.game, "/")
	if base == "" {
		return errors.New("-game is required")
	}

	phones := make([]*phone, o.players)
	for i := range phones {
		p := newPhone(i, base, o, logLine)
		if err := p.join(ctx); err != nil {
			return fmt.Errorf("phone %d join: %w", i, err)
		}
		p.listen(ctx)
		phones[i] = p
	}
	if !o.host {
		<-ctx.Done()
		return nil
	}
	hc, err := hostConn(ctx, base, o.session)
	if err != nil {
		return err
	}
	h := &hostBot{c: hc, o: o, log: logLine}
	if o.churn && len(phones) >= 3 {
		h.onRacing = func() { go churn(ctx, phones[1], phones[2], o.churnAway, hc) }
	}
	return h.run(ctx)
}

func newPhone(i int, base string, o opts, logLine func(string, ...any)) *phone {
	name := names[i%len(names)]
	if i >= len(names) {
		name = fmt.Sprintf("%s %d", name, i/len(names)+1)
	}
	return &phone{n: i, name: name, style: styles[i%len(styles)], color: colors[i%len(colors)], hat: hats[i%len(hats)],
		poll: o.pollEvery > 0 && i%o.pollEvery == o.pollEvery-1,
		c:    &conn{base: base, client: newJar()}, log: logLine}
}

func openLog(path string) (func(string, ...any), func(), error) {
	var mu sync.Mutex
	f := os.Stdout
	if path != "" {
		var err error
		if f, err = os.Create(path); err != nil {
			return nil, nil, fmt.Errorf("creating log: %w", err)
		}
	}
	logLine := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(f, "%s  "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
	}
	return logLine, func() {
		if f != os.Stdout {
			f.Close()
		}
	}, nil
}
