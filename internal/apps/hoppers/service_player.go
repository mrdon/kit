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
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/livegame"
)

// ErrBadRequest is input a person can fix (an empty name, an unknown pack).
var ErrBadRequest = errors.New("hoppers: bad request")

// maxNameRunes matches the engine's limit, counted in characters so a name
// with an emoji in it is never cut mid-character.
const maxNameRunes = 24

// Frog is what a phone sends to join or to edit its frog.
type Frog struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Hat   string `json:"hat"`
}

func (f Frog) clean() (Frog, error) {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return f, fmt.Errorf("%w: your frog needs a name", ErrBadRequest)
	}
	if utf8.RuneCountInString(f.Name) > maxNameRunes {
		f.Name = string([]rune(f.Name)[:maxNameRunes])
	}
	return f, nil
}

// Join adds a phone's frog to the game, or edits it when the phone already
// has one (playerID from its cookie, uuid.Nil for a new phone). A new phone
// gets a fresh token, which the caller sets as its cookie; an edit returns "".
func (s *Service) Join(ctx context.Context, tenantID, gameID, playerID uuid.UUID, frog Frog) (*Player, string, error) {
	frog, err := frog.clean()
	if err != nil {
		return nil, "", err
	}
	var player *Player
	var token string
	_, err = s.change(ctx, tenantID, gameID, func(tx pgx.Tx, g *engine.Game, now time.Time) error {
		if playerID != uuid.Nil {
			p, err := GetPlayer(ctx, tx, tenantID, gameID, playerID)
			if err != nil {
				return err
			}
			if err := setPlayerName(ctx, tx, tenantID, gameID, p.ID, frog.Name); err != nil {
				return err
			}
			player = p
			player.Name = frog.Name
		} else {
			token = livegame.NewToken()
			if token == "" {
				return errors.New("hoppers: could not mint a player token")
			}
			p, err := insertPlayer(ctx, tx, tenantID, gameID, frog.Name, livegame.HashToken(token), now)
			if err != nil {
				return err
			}
			player = p
		}
		_, err := g.Apply(engine.Command{
			Type: engine.CmdJoin, Player: player.ID.String(),
			Name: frog.Name, Color: frog.Color, Hat: frog.Hat,
		}, now)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return player, token, nil
}

// IssueReclaim is the host handing a phone-less player a four-digit code.
// Its hash replaces the player's token, which also signs the lost phone out.
func (s *Service) IssueReclaim(ctx context.Context, tenantID, gameID, playerID uuid.UUID, code string) error {
	return SetPlayerToken(ctx, s.pool, tenantID, gameID, playerID,
		livegame.HashToken(livegame.ReclaimSecret(gameID, playerID, code)))
}

// RedeemReclaim exchanges a host-issued code for a fresh identity token. The
// code is burned on use: four digits read out loud must not stay live.
func (s *Service) RedeemReclaim(ctx context.Context, tenantID, gameID, playerID uuid.UUID, code string) (string, error) {
	p, err := FindPlayerByToken(ctx, s.pool, tenantID, gameID, playerID,
		livegame.HashToken(livegame.ReclaimSecret(gameID, playerID, code)))
	if err != nil {
		return "", err
	}
	token := livegame.NewToken()
	if token == "" {
		return "", errors.New("hoppers: could not mint a player token")
	}
	if err := SetPlayerToken(ctx, s.pool, tenantID, gameID, p.ID, livegame.HashToken(token)); err != nil {
		return "", err
	}
	return token, nil
}

// PackGenerated is the default night: a fresh cup drawn from the built-in
// levels, the way the standalone server always ran.
const PackGenerated = "generated"

// NewGame is what a host picks when creating a night.
type NewGame struct {
	Title string `json:"title"`
	// Pack is a built-in pack (engine.BuiltinPacks) or PackGenerated.
	Pack string `json:"pack"`
}

// CreateGame makes a night in the lobby.
func (s *Service) CreateGame(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, req NewGame) (*Game, error) {
	seed := randomSeed()
	cfg, err := packConfig(req.Pack, seed)
	if err != nil {
		return nil, err
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
	row := &Game{
		TenantID: tenantID, Name: name, Title: strings.TrimSpace(req.Title), Pack: pack,
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

// packConfig is the night a pack name stands for.
func packConfig(pack string, seed uint64) (engine.GameConfig, error) {
	if pack == "" || pack == PackGenerated {
		cfg, err := engine.Generate(mrand.New(mrand.NewPCG(seed, 0xc0b)), engine.DefaultPreferences(), nil)
		if err != nil {
			return cfg, fmt.Errorf("generating a hoppers cup: %w", err)
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
