package hoppers

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/livegame"
)

// ErrConflict is a host press made against a stage the game has already
// left: the double tap, or two hosts pressing at once.
var ErrConflict = errors.New("hoppers: the game already moved on")

// PackGenerated is the default night: a fresh cup drawn from the built-in
// levels, the way the standalone server always ran.
const PackGenerated = "generated"

// NewGame is what a host picks when creating a night: a pack, and the few
// rules worth changing at the bar. A zero tweak keeps the pack's value.
type NewGame struct {
	Title string `json:"title"`
	// Pack is a built-in pack (engine.BuiltinPacks) or PackGenerated.
	Pack  string     `json:"pack"`
	Rules RuleTweaks `json:"rules"`
}

// RuleTweaks are the rules a host may change. Everything else about a night
// is the pack's.
type RuleTweaks struct {
	Rounds       int `json:"rounds,omitempty"`       // races per night (rounds)
	HeatSize     int `json:"heatSize,omitempty"`     // most people per race
	MinFrogs     int `json:"minFrogs,omitempty"`     // bots fill a race up to this many frogs
	FlickSeconds int `json:"flickSeconds,omitempty"` // before a frog hops by itself
}

// CreateGame makes a night in the lobby.
func (s *Service) CreateGame(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, req NewGame) (*Game, error) {
	seed := randomSeed()
	cfg, err := packConfig(req.Pack, req.Rules.Rounds, seed)
	if err != nil {
		return nil, err
	}
	req.Rules.apply(&cfg.Rules)
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}
	g := engine.NewGameWith(cfg, engine.Options{SeenWindow: engine.DefaultSeenWindow, Speed: s.speed}, seed)
	g.Advance(s.now().UTC())
	blob, err := g.Save()
	if err != nil {
		return nil, fmt.Errorf("saving new hoppers game: %w", err)
	}
	name, err := livegame.UniqueName(func(n string) (bool, error) { return nameTaken(ctx, s.pool, tenantID, n) })
	if err != nil {
		return nil, err
	}
	pack := req.Pack
	if pack == "" {
		pack = PackGenerated
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Frog race"
	}
	row := &Game{
		TenantID: tenantID, Name: name, Title: title, Pack: pack,
		Engine: blob, StateVersion: int64(g.Version()), Phase: string(g.Phase()), CreatedBy: createdBy,
	}
	var out *Game
	_, err = livegame.AssignJoinCode(func(code string) error {
		row.JoinCode = code
		var err error
		out, err = insertGame(ctx, s.pool, row)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("hoppers: %w", err)
	}
	return out, nil
}

func (t RuleTweaks) apply(r *engine.Rules) {
	if t.Rounds > 0 {
		r.Rounds = t.Rounds
	}
	if t.HeatSize > 0 {
		r.HeatSize = t.HeatSize
	}
	if t.MinFrogs > 0 {
		r.MinFrogs = t.MinFrogs
	}
	if t.FlickSeconds > 0 {
		r.FlickTimeout = time.Duration(t.FlickSeconds) * time.Second
	}
}

// packConfig is the night a pack name stands for. A generated cup is drawn
// for the night's rounds, so it has one level per race.
func packConfig(pack string, rounds int, seed uint64) (engine.GameConfig, error) {
	if pack == "" || pack == PackGenerated {
		prefs := engine.DefaultPreferences()
		prefs.Rounds = rounds
		cfg, err := engine.Generate(mrand.New(mrand.NewPCG(seed, 0xc0b)), prefs, nil)
		if err != nil {
			return cfg, fmt.Errorf("%w: %w", ErrBadRequest, err)
		}
		return cfg, nil
	}
	cfg, err := engine.BuiltinPack(pack)
	if err != nil {
		return cfg, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}
	return cfg, nil
}

// randomSeed seeds a night's engine. crypto/rand so two games created in the
// same instant (or by two processes) never race the same way.
func randomSeed() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint64(b[:])
}

// Stage is where a game is, as far as the host button cares. The host's
// press carries the stage it was made from, so a double tap (or two hosts
// at once) cannot skip a heat: the second press finds the game somewhere
// else and is refused.
type Stage struct {
	Phase engine.Phase `json:"phase"`
	Sub   engine.Sub   `json:"sub"`
	Round int          `json:"round"`
	Heat  int          `json:"heat"`
}

func stageOf(s engine.State) Stage {
	return Stage{Phase: s.Phase, Sub: s.Sub, Round: s.Round, Heat: s.Heat}
}

// HostAdvance is the host's one button: start, next heat, next round, final
// scores, new game -- whatever the engine says it does right now -- pressed
// from the stage the host saw.
func (s *Service) HostAdvance(ctx context.Context, tenantID, gameID uuid.UUID, from Stage) (*Snapshot, error) {
	return s.change(ctx, tenantID, gameID, func(_ pgx.Tx, g *engine.Game, now time.Time) error {
		if stageOf(g.View(engine.RoleTV, "")) != from {
			return fmt.Errorf("%w: %w", errRefused, ErrConflict)
		}
		if _, err := g.Apply(engine.Command{Type: engine.CmdHost}, now); err != nil {
			return fmt.Errorf("%w: %w", errRefused, err)
		}
		return nil
	})
}
