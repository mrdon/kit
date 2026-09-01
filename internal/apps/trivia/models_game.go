package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Game is one quiz night.
//
// StateVersion is the spine of the live layer: bumped inside the same
// transaction as every mutation, it is the SSE frame id, the poll fallback's
// cursor and the display's staleness watchdog at once. PhaseDeadline is an
// absolute server timestamp -- never a duration, never client-supplied --
// which is what makes the host a controller rather than an authority.
type Game struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Name           string
	Title          string
	Phase          Phase
	BoardRows      int
	BoardColumns   int
	CellValues     []int
	TokenValues    []int
	FinalWager     bool
	AnswerSeconds  int
	RevealSeconds  int
	BetSeconds     int
	CurrentRoundID *uuid.UUID
	PhaseDeadline  *time.Time
	StateVersion   int64
	CreatedBy      *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// JoinCode is the short, workspace-less code the QR encodes. The long
	// /{slug}/trivia/{name} URL keeps working; this exists only to make the
	// code on the wall scannable from further away.
	JoinCode string
}

// Settings are the per-game knobs. Board size, values and the final are
// settings, never a "game mode": a quick game and a long game are the same
// code with different numbers, and that is what stops a second ruleset from
// ever needing to exist.
type Settings struct {
	Title         string `json:"title"`
	BoardRows     int    `json:"board_rows"`
	BoardColumns  int    `json:"board_columns"`
	CellValues    []int  `json:"cell_values"`
	TokenValues   []int  `json:"token_values"`
	FinalWager    bool   `json:"final_wager"`
	AnswerSeconds int    `json:"answer_seconds"`
	RevealSeconds int    `json:"reveal_seconds"`
	BetSeconds    int    `json:"bet_seconds"`
}

const gameColumns = `id, tenant_id, name, title, phase, board_rows, board_columns,
	cell_values, token_values, final_wager, answer_seconds, reveal_seconds, bet_seconds,
	current_round_id, phase_deadline, state_version, created_by, created_at, updated_at,
	COALESCE(join_code, '')`

// gameColumnsQualified is the same list with a table alias, for the one query
// that joins tenants.
const gameColumnsQualified = `g.id, g.tenant_id, g.name, g.title, g.phase, g.board_rows, g.board_columns,
	g.cell_values, g.token_values, g.final_wager, g.answer_seconds, g.reveal_seconds, g.bet_seconds,
	g.current_round_id, g.phase_deadline, g.state_version, g.created_by, g.created_at, g.updated_at,
	COALESCE(g.join_code, '')`

func scanGame(row pgx.Row) (*Game, error) {
	var g Game
	err := row.Scan(&g.ID, &g.TenantID, &g.Name, &g.Title, &g.Phase,
		&g.BoardRows, &g.BoardColumns, &g.CellValues, &g.TokenValues, &g.FinalWager,
		&g.AnswerSeconds, &g.RevealSeconds, &g.BetSeconds,
		&g.CurrentRoundID, &g.PhaseDeadline, &g.StateVersion,
		&g.CreatedBy, &g.CreatedAt, &g.UpdatedAt, &g.JoinCode)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGame inserts a game in setup with the given settings.
func CreateGame(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string, s Settings, createdBy *uuid.UUID) (*Game, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		INSERT INTO app_trivia_games
		    (tenant_id, name, title, phase, board_rows, board_columns, cell_values, token_values,
		     final_wager, answer_seconds, reveal_seconds, bet_seconds, created_by, join_code)
		VALUES ($1,$2,$3,'lobby',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING `+gameColumns,
		tenantID, name, s.Title, s.BoardRows, s.BoardColumns, s.CellValues, s.TokenValues,
		s.FinalWager, s.AnswerSeconds, s.RevealSeconds, s.BetSeconds, createdBy, NewJoinCode()))
	if err != nil {
		return nil, fmt.Errorf("inserting trivia game: %w", err)
	}
	return g, nil
}

// GetGame loads a game by id.
func GetGame(ctx context.Context, q Querier, tenantID, id uuid.UUID) (*Game, error) {
	g, err := scanGame(q.QueryRow(ctx,
		`SELECT `+gameColumns+` FROM app_trivia_games WHERE tenant_id = $1 AND id = $2`,
		tenantID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying trivia game: %w", err)
	}
	return g, nil
}

// GetGameByName loads a game by its three-word public name. This is the
// lookup every phone and TV does, and it is tenant-scoped like everything
// else -- a name in one workspace is invisible from another.
func GetGameByName(ctx context.Context, q Querier, tenantID uuid.UUID, name string) (*Game, error) {
	g, err := scanGame(q.QueryRow(ctx,
		`SELECT `+gameColumns+` FROM app_trivia_games WHERE tenant_id = $1 AND name = $2`,
		tenantID, name))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying trivia game by name: %w", err)
	}
	return g, nil
}

// ListGames returns the tenant's games, newest first.
func ListGames(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, limit int) ([]*Game, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+gameColumns+` FROM app_trivia_games WHERE tenant_id = $1
		  ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing trivia games: %w", err)
	}
	defer rows.Close()
	var out []*Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning trivia game: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateSettings rewrites a game's knobs. Refused outside setup and lobby by
// the service layer -- changing the cell values of a board already in play
// would restate scores that have been read off a TV.
func UpdateSettings(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, s Settings) (*Game, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET title = $3, board_rows = $4, board_columns = $5, cell_values = $6,
		       token_values = $7, final_wager = $8, answer_seconds = $9,
		       reveal_seconds = $10, bet_seconds = $11,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2
		RETURNING `+gameColumns,
		tenantID, id, s.Title, s.BoardRows, s.BoardColumns, s.CellValues, s.TokenValues,
		s.FinalWager, s.AnswerSeconds, s.RevealSeconds, s.BetSeconds))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating trivia game settings: %w", err)
	}
	return g, nil
}

// DeleteGame removes a game and everything under it.
func DeleteGame(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	tag, err := pool.Exec(ctx,
		`DELETE FROM app_trivia_games WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting trivia game: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BoardCell is one tile: a topic column, a points row, and the question that
// tile will ask.
type BoardCell struct {
	ID         uuid.UUID
	GameID     uuid.UUID
	RoundIndex int
	ColIndex   int
	RowIndex   int
	Topic      string
	Points     int
	QuestionID uuid.UUID
	PlayedAt   *time.Time
}

// ReplaceBoard writes a game's board in one transaction, clearing whatever
// was there. Building a board is a setup-time act -- the host sees the whole
// grid before the doors open -- so a rebuild replacing the previous attempt
// wholesale is the honest operation.
//
// It also stamps last_used_at on every question that landed on the board, so
// the next board prefers what the room has not heard.
func ReplaceBoard(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, cells []BoardCell) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning board transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM app_trivia_board_cells WHERE tenant_id = $1 AND game_id = $2`,
		tenantID, gameID); err != nil {
		return fmt.Errorf("clearing board cells: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(cells))
	for _, c := range cells {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_trivia_board_cells
			    (tenant_id, game_id, round_index, col_index, row_index, topic, points, question_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenantID, gameID, c.RoundIndex, c.ColIndex, c.RowIndex, c.Topic, c.Points, c.QuestionID); err != nil {
			return fmt.Errorf("inserting board cell: %w", err)
		}
		ids = append(ids, c.QuestionID)
	}
	if err := MarkQuestionsUsed(ctx, tx, tenantID, ids); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE app_trivia_games SET state_version = state_version + 1, updated_at = now()
		  WHERE tenant_id = $1 AND id = $2`, tenantID, gameID); err != nil {
		return fmt.Errorf("bumping state version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing board: %w", err)
	}
	return nil
}

// ListBoardCells returns a game's board in display order.
func ListBoardCells(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]BoardCell, error) {
	rows, err := q.Query(ctx, `
		SELECT id, game_id, round_index, col_index, row_index, topic, points, question_id, played_at
		  FROM app_trivia_board_cells
		 WHERE tenant_id = $1 AND game_id = $2
		 ORDER BY round_index, row_index, col_index`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing board cells: %w", err)
	}
	defer rows.Close()
	var out []BoardCell
	for rows.Next() {
		var c BoardCell
		if err := rows.Scan(&c.ID, &c.GameID, &c.RoundIndex, &c.ColIndex, &c.RowIndex,
			&c.Topic, &c.Points, &c.QuestionID, &c.PlayedAt); err != nil {
			return nil, fmt.Errorf("scanning board cell: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Team is one table of people sharing a phone.
type Team struct {
	ID                  uuid.UUID
	GameID              uuid.UUID
	Name                string
	NameKey             string
	EligibleFromOrdinal int
	JoinedAt            time.Time
}

// InsertTeam adds a team. The unique index on (tenant, game, name_key) is the
// guard, not a read-then-write: two phones typing the same name at the same
// moment would both pass a check-first.
func InsertTeam(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, name, tokenHash string, eligibleFrom int) (*Team, error) {
	var t Team
	err := pool.QueryRow(ctx, `
		INSERT INTO app_trivia_teams (tenant_id, game_id, name, name_key, token_hash, eligible_from_ordinal)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, game_id, name, name_key, eligible_from_ordinal, joined_at`,
		tenantID, gameID, name, FoldKey(name), tokenHash, eligibleFrom).
		Scan(&t.ID, &t.GameID, &t.Name, &t.NameKey, &t.EligibleFromOrdinal, &t.JoinedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTeams returns a game's teams in join order -- which is the order the
// lobby pills entered on the TV, so the room sees a stable list.
func ListTeams(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]Team, error) {
	rows, err := q.Query(ctx, `
		SELECT id, game_id, name, name_key, eligible_from_ordinal, joined_at
		  FROM app_trivia_teams WHERE tenant_id = $1 AND game_id = $2 ORDER BY joined_at, id`,
		tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing teams: %w", err)
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.GameID, &t.Name, &t.NameKey, &t.EligibleFromOrdinal, &t.JoinedAt); err != nil {
			return nil, fmt.Errorf("scanning team: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindTeamByToken resolves the team behind a cookie. The cookie carries the
// team id and a nonce; only the hash is stored, so this compares hashes.
func FindTeamByToken(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID, teamID uuid.UUID, tokenHash string) (*Team, error) {
	var t Team
	err := pool.QueryRow(ctx, `
		SELECT id, game_id, name, name_key, eligible_from_ordinal, joined_at
		  FROM app_trivia_teams
		 WHERE tenant_id = $1 AND game_id = $2 AND id = $3 AND token_hash = $4`,
		tenantID, gameID, teamID, tokenHash).
		Scan(&t.ID, &t.GameID, &t.Name, &t.NameKey, &t.EligibleFromOrdinal, &t.JoinedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolving team by token: %w", err)
	}
	return &t, nil
}

// SetTeamToken replaces a team's stored token hash. This is the host-issued
// reclaim path: a table whose phone died taps its name in the console and the
// host reads them a code, which mints a fresh cookie. The trust boundary sits
// with the person standing in the room who can see who is asking -- which is
// why there is no "pick your team from this list" on the phone.
func SetTeamToken(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID, teamID uuid.UUID, tokenHash string) error {
	tag, err := pool.Exec(ctx,
		`UPDATE app_trivia_teams SET token_hash = $4 WHERE tenant_id = $1 AND game_id = $2 AND id = $3`,
		tenantID, gameID, teamID, tokenHash)
	if err != nil {
		return fmt.Errorf("setting team token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountTeams is the MaxTeams check's input.
func CountTeams(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM app_trivia_teams WHERE tenant_id = $1 AND game_id = $2`,
		tenantID, gameID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting teams: %w", err)
	}
	return n, nil
}
