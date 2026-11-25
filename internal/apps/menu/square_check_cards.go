package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CardSurface is how the Square check reaches decision cards and briefings
// without importing the cards app: cmd/kit wires an adapter in, as it does
// for the vault. Nil files nothing, which is what tests and a workspace
// without cards get.
type CardSurface interface {
	CreateDecision(ctx context.Context, tenantID uuid.UUID, in CardDecision) (uuid.UUID, error)
	CreateBriefing(ctx context.Context, tenantID uuid.UUID, title, body string, roles []string) error
}

// CardDecision is a decision card as the check files it.
type CardDecision struct {
	Title       string
	Body        string
	Roles       []string
	Recommended string
	Options     []CardOption
}

// CardOption is one choice. ToolName empty means choosing it does nothing.
type CardOption struct {
	ID       string
	Label    string
	ToolName string
	Args     json.RawMessage
}

// ConfigureCards wires the card surface. Call once from main.
func ConfigureCards(cards CardSurface) {
	if instance != nil {
		instance.cards = cards
	}
}

// squareCardRoles is who the check asks: the people who can change Square.
var squareCardRoles = []string{"admin"}

// fileQuestions puts each question not asked before on a decision card, or
// a briefing for a notice, and reports how many it filed. A question is recorded before its card is made,
// so a failure part way never asks twice; at worst it never asks, and the
// preview still shows it.
func (a *App) fileQuestions(ctx context.Context, tenantID uuid.UUID, qs []sqQuestion) (int, error) {
	if a.cards == nil {
		return 0, nil
	}
	filed := 0
	for _, q := range qs {
		fresh, err := claimQuestion(ctx, a.pool, tenantID, q.Key)
		if err != nil {
			return filed, err
		}
		if !fresh {
			continue
		}
		if q.Notice {
			if err := a.cards.CreateBriefing(ctx, tenantID, q.Title, q.Body, squareCardRoles); err != nil {
				return filed, fmt.Errorf("filing %q: %w", q.Title, err)
			}
			filed++
			continue
		}
		card, err := questionCard(q)
		if err != nil {
			return filed, err
		}
		id, err := a.cards.CreateDecision(ctx, tenantID, card)
		if err != nil {
			return filed, fmt.Errorf("filing %q: %w", q.Title, err)
		}
		if err := noteQuestionCard(ctx, a.pool, tenantID, q.Key, id); err != nil {
			return filed, err
		}
		filed++
	}
	return filed, nil
}

func questionCard(q sqQuestion) (CardDecision, error) {
	card := CardDecision{Title: q.Title, Body: q.Body, Roles: squareCardRoles, Recommended: q.Recommended}
	for _, o := range q.Options {
		opt := CardOption{ID: o.ID, Label: o.Label}
		if o.Fix != nil {
			args, err := json.Marshal(map[string]any{"fix": o.Fix})
			if err != nil {
				return card, fmt.Errorf("encoding fix: %w", err)
			}
			opt.ToolName, opt.Args = applyFixTool, args
		}
		card.Options = append(card.Options, opt)
	}
	return card, nil
}

// briefAdds tells the admins what the check changed in Square.
func (a *App) briefAdds(ctx context.Context, tenantID uuid.UUID, done, counted []string) error {
	if a.cards == nil || len(done)+len(counted) == 0 {
		return nil
	}
	var parts []string
	if len(done) > 0 {
		parts = append(parts, "The nightly check found these on the menu but not in Square, and added them:\n\n- "+
			strings.Join(done, "\n- "))
	}
	if len(counted) > 0 {
		parts = append(parts, "It kept the online crowlers and kegs, and the flight picker, in step with the menu:\n\n- "+
			strings.Join(counted, "\n- "))
	}
	return a.cards.CreateBriefing(ctx, tenantID, "Square updated from the menu", strings.Join(parts, "\n\n"), squareCardRoles)
}

// claimQuestion records a question key, reporting false if it was already
// asked.
func claimQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (bool, error) {
	var got string
	err := pool.QueryRow(ctx,
		`INSERT INTO app_menu_square_asked (tenant_id, key) VALUES ($1, $2)
		 ON CONFLICT (tenant_id, key) DO NOTHING RETURNING key`, tenantID, key).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recording square question: %w", err)
	}
	return true, nil
}

func noteQuestionCard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string, cardID uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_menu_square_asked SET card_id = $3 WHERE tenant_id = $1 AND key = $2`, tenantID, key, cardID)
	if err != nil {
		return fmt.Errorf("recording square question card: %w", err)
	}
	return nil
}

// askedBefore reports which of the keys already have a card, for the preview.
func askedBefore(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT key FROM app_menu_square_asked WHERE tenant_id = $1 AND key = ANY($2)`, tenantID, keys)
	if err != nil {
		return nil, fmt.Errorf("reading square questions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("reading square question: %w", err)
		}
		out[k] = true
	}
	return out, rows.Err()
}
