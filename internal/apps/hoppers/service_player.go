package hoppers

import (
	"context"
	"errors"
	"fmt"
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
