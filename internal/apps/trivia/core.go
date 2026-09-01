package trivia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// dispatchCore is the one implementation of both trivia tools. The agent
// registry and the MCP server are thin wrappers over this function, which is
// what keeps the two surfaces byte-identical by construction rather than by
// two hand-maintained switches that drift apart.
func dispatchCore(ctx context.Context, caller *services.Caller, pool *pgxpool.Pool, svc *Service, name string, raw json.RawMessage) (string, error) {
	if caller == nil {
		return "", errors.New("trivia: no caller on context")
	}
	switch name {
	case "trivia_status":
		return coreStatus(ctx, caller, pool, svc, raw)
	case "trivia_results":
		return coreResults(ctx, caller, pool, svc, raw)
	default:
		return "", fmt.Errorf("trivia: unknown tool %q", name)
	}
}

type statusInput struct {
	Limit int `json:"limit"`
}

func coreStatus(ctx context.Context, caller *services.Caller, pool *pgxpool.Pool, svc *Service, raw json.RawMessage) (string, error) {
	var in statusInput
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	if in.Limit <= 0 || in.Limit > 25 {
		in.Limit = 5
	}
	games, err := ListGames(ctx, pool, caller.TenantID, in.Limit)
	if err != nil {
		return "", err
	}
	if len(games) == 0 {
		return "No trivia games yet.", nil
	}

	var b strings.Builder
	for _, g := range games {
		snap, err := svc.Snapshot(ctx, caller.TenantID, g.ID)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "*%s* (%s) — %s\n", displayTitle(g), g.Name, phaseSentence(snap))
		if len(snap.Teams) == 0 {
			b.WriteString("  no teams joined\n")
			continue
		}
		leader := leadingTeam(snap)
		fmt.Fprintf(&b, "  %d team%s", len(snap.Teams), plural(len(snap.Teams)))
		if leader != nil {
			fmt.Fprintf(&b, " · leading: %s on %s", leader.Name, FormatMoney(leader.Score))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

type resultsInput struct {
	Game string `json:"game"`
}

func coreResults(ctx context.Context, caller *services.Caller, pool *pgxpool.Pool, svc *Service, raw json.RawMessage) (string, error) {
	var in resultsInput
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	game, err := resolveGameForTool(ctx, pool, caller.TenantID, strings.TrimSpace(in.Game))
	if err != nil {
		return "", err
	}
	snap, err := svc.Snapshot(ctx, caller.TenantID, game.ID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*%s* (%s) — %s\n\n", displayTitle(game), game.Name, phaseSentence(snap))

	if len(snap.Teams) == 0 {
		b.WriteString("Nobody joined this one.")
		return b.String(), nil
	}

	standings := append([]SnapTeam(nil), snap.Teams...)
	sort.Slice(standings, func(i, j int) bool { return standings[i].Score > standings[j].Score })
	b.WriteString("*Final standings*\n")
	for i, t := range standings {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, t.Name, FormatMoney(t.Score))
	}

	recap, err := roundRecap(ctx, pool, caller.TenantID, game, snap)
	if err != nil {
		return "", err
	}
	if recap != "" {
		b.WriteString("\n*Round by round*\n")
		b.WriteString(recap)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// roundRecap lists each question with its answer and who took the cell. The
// answer is safe here by definition: this is a finished game being asked
// about the next morning, not a live one.
func roundRecap(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, game *Game, snap *Snapshot) (string, error) {
	rounds, err := ListRounds(ctx, pool, tenantID, game.ID)
	if err != nil {
		return "", err
	}
	if len(rounds) == 0 {
		return "", nil
	}
	scores, err := ScoredRoundScores(ctx, pool, tenantID, game.ID)
	if err != nil {
		return "", err
	}
	byRound := map[uuid.UUID][]RoundScore{}
	for _, s := range scores {
		byRound[s.RoundID] = append(byRound[s.RoundID], s)
	}
	names := map[uuid.UUID]string{}
	for _, t := range snap.Teams {
		names[t.ID] = t.Name
	}

	var b strings.Builder
	for _, r := range rounds {
		label := fmt.Sprintf("%d.", r.Ordinal)
		if r.IsFinal {
			label = "Final:"
		}
		// The round's own copy, so a recap of a night from last month still
		// reads correctly after the dataset behind it was deleted.
		answer := r.AnswerText
		if answer == "" {
			answer = FormatValue(r.AnswerValue)
		}
		fmt.Fprintf(&b, "%s %s — %s", label, r.Prompt, answer)
		var took []string
		for _, s := range byRound[r.ID] {
			if s.BoardPoints > 0 {
				took = append(took, names[s.TeamID])
			}
		}
		sort.Strings(took)
		switch {
		case r.ScoredAt == nil:
			b.WriteString(" (not scored)")
		case len(took) > 0:
			fmt.Fprintf(&b, " (%s took %s)", strings.Join(took, " and "), FormatMoney(r.Points))
		default:
			b.WriteString(" (nobody had it)")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// resolveGameForTool finds a game by name, or the most recent one when the
// caller did not name one.
func resolveGameForTool(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (*Game, error) {
	if name != "" {
		if !IsValidGameName(name) {
			return nil, fmt.Errorf("%q is not a game name — they look like brave-otter-lamp", name)
		}
		return GetGameByName(ctx, pool, tenantID, name)
	}
	games, err := ListGames(ctx, pool, tenantID, 1)
	if err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, errors.New("there are no trivia games yet")
	}
	return games[0], nil
}

func displayTitle(g *Game) string {
	if g.Title != "" {
		return g.Title
	}
	return "Trivia"
}

// phaseSentence turns a phase into something worth reading in Slack.
func phaseSentence(snap *Snapshot) string {
	played, total := 0, len(snap.Board)
	for _, c := range snap.Board {
		if c.Played {
			played++
		}
	}
	switch snap.Phase {
	case PhaseSetup:
		return "not opened yet"
	case PhaseLobby:
		return "teams joining"
	case PhasePodium:
		return "finished"
	case PhaseBoard, PhaseQuestion, PhaseReveal, PhaseBetting, PhaseScoring:
		return fmt.Sprintf("in play, %d of %d questions done", played, total)
	}
	return string(snap.Phase)
}

func leadingTeam(snap *Snapshot) *SnapTeam {
	var best *SnapTeam
	for i := range snap.Teams {
		if best == nil || snap.Teams[i].Score > best.Score {
			best = &snap.Teams[i]
		}
	}
	return best
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
