// Package agents has AI players for Hoppers and a headless harness that plays
// whole nights against the engine on a virtual clock (no rendering, no
// sleeping), so thousands of games can be compared in seconds.
//
// Strategies decide when to flick on your frog's turn (an instant bot, a
// person in a noisy bar, someone who wandered off), and "exec:<cmd>" hands the
// decision to an external program. Flick skill (how often a flick lands in the
// phone's green band for +1) is per seat: Seat.Skill.
package agents

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// Agent decides what a player does when it's their frog's turn. It gets the
// live game (read-only: use Snapshot, Leader, Clone for lookahead) and
// returns commands for its own player and how long after now to send them
// (a person's reaction time). No commands means the frog auto-hops when the
// flick timer runs out.
type Agent interface {
	Act(g *engine.Game, me string) (cmds []engine.Command, after time.Duration)
}

// Factory builds a fresh agent per seat per game.
type Factory func(rng *rand.Rand) (Agent, error)

// Registry maps strategy names to factories. "exec:<cmd>" is handled by New.
var Registry = map[string]Factory{
	"instant": func(*rand.Rand) (Agent, error) { return instant{}, nil },
	"human":   func(r *rand.Rand) (Agent, error) { return &human{rng: r}, nil },
	"asleep":  func(*rand.Rand) (Agent, error) { return asleep{}, nil },
}

// Names lists the built-in strategies.
func Names() []string {
	var n []string
	for k := range Registry {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// New builds an agent by name; "exec:./my-agent --flag" runs an external
// program (e.g. an LLM wrapper or a flick-skill model).
func New(name string, rng *rand.Rand) (Agent, error) {
	if cmd, ok := strings.CutPrefix(name, "exec:"); ok {
		return newExec(cmd)
	}
	f, ok := Registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown strategy %q (have %v, or exec:<cmd>)", name, Names())
	}
	return f(rng)
}

func flick(me string) []engine.Command {
	return []engine.Command{{Type: engine.CmdFlick, Player: me}}
}

// instant flicks the moment it's its turn.
type instant struct{}

func (instant) Act(_ *engine.Game, me string) ([]engine.Command, time.Duration) { return flick(me), 0 }

// human is a person in a bar: looks up from their pint, finds the phone,
// flicks 1-4s later, and now and then misses their turn entirely.
type human struct{ rng *rand.Rand }

func (a *human) Act(_ *engine.Game, me string) ([]engine.Command, time.Duration) {
	if a.rng.Float64() < 0.05 {
		return nil, 0
	}
	return flick(me), time.Duration(1000+a.rng.IntN(3000)) * time.Millisecond
}

// asleep never flicks: every hop is an auto-hop at the flick timeout.
type asleep struct{}

func (asleep) Act(*engine.Game, string) ([]engine.Command, time.Duration) { return nil, 0 }

// execAgent talks to an external program over stdin/stdout, one JSON line
// each way: on each of its turns we send {"me": id, "state": <snapshot>}, it
// replies with {"after_ms": 1200, "commands": [...]} or just a JSON array of
// commands (Player is filled in for it).
type execAgent struct {
	mu  sync.Mutex
	in  *json.Encoder
	out *bufio.Scanner
}

func newExec(cmdline string) (Agent, error) {
	cmd := exec.Command("sh", "-c", cmdline)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("exec agent %q: stdin: %w", cmdline, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("exec agent %q: stdout: %w", cmdline, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec agent %q: start: %w", cmdline, err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	return &execAgent{in: json.NewEncoder(stdin), out: sc}, nil
}

func (a *execAgent) Act(g *engine.Game, me string) ([]engine.Command, time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.in.Encode(map[string]any{"me": me, "state": g.Snapshot()}); err != nil {
		slog.Warn("exec agent: send", "err", err)
		return nil, 0
	}
	if !a.out.Scan() {
		slog.Warn("exec agent: no reply", "err", a.out.Err())
		return nil, 0
	}
	var reply struct {
		AfterMS  int              `json:"after_ms"`
		Commands []engine.Command `json:"commands"`
	}
	line := a.out.Bytes()
	if json.Unmarshal(line, &reply.Commands) != nil {
		if err := json.Unmarshal(line, &reply); err != nil {
			slog.Warn("exec agent: bad reply", "line", string(line), "err", err)
			return nil, 0
		}
	}
	for i := range reply.Commands {
		reply.Commands[i].Player = me
	}
	return reply.Commands, time.Duration(reply.AfterMS) * time.Millisecond
}
