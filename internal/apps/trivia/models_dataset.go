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

// Dataset is a named collection of questions. It is the ONLY concept for
// "a set of questions" — an upload creates one, the shipped starter pack is
// seeded as one, and a game draws its board from one or more of them. There
// is no separate bank and nothing reads questions from anywhere else.
type Dataset struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Notes      string    `json:"notes"`
	BuiltinKey string    `json:"builtin_key"`
	Questions  int       `json:"questions"`
	Topics     int       `json:"topics"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ErrDatasetInUse means a dataset's questions are on the board of a game that
// has not finished, so removing it would pull a question out from under a
// night in progress.
//
// A FINISHED game does not block anything. Its rounds carry their own copy of
// every question they asked, so the recap survives the dataset. Without that,
// a set became permanently undeletable the moment one game used it, and a
// venue running a weekly quiz would accumulate sets it could never remove.
var ErrDatasetInUse = errors.New("trivia: that dataset is in use by a game still in play")

// DatasetInUse reports whether an unfinished game's board depends on this
// dataset's questions. "Unfinished" is a phase, not a foreign key, which is
// why this is a service-layer check rather than an ON DELETE RESTRICT.
func DatasetInUse(ctx context.Context, q Querier, tenantID, datasetID uuid.UUID) (string, error) {
	var name string
	err := q.QueryRow(ctx, `
		SELECT g.name
		  FROM app_trivia_board_cells c
		  JOIN app_trivia_questions qs ON qs.id = c.question_id AND qs.tenant_id = c.tenant_id
		  JOIN app_trivia_games g ON g.id = c.game_id AND g.tenant_id = c.tenant_id
		 WHERE c.tenant_id = $1 AND qs.dataset_id = $2 AND g.phase <> 'podium'
		 LIMIT 1`, tenantID, datasetID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("checking dataset use: %w", err)
	}
	return name, nil
}

// ListDatasets returns the workspace's datasets with their question and topic
// counts, so the picker can show what selecting one would actually buy.
func ListDatasets(ctx context.Context, q Querier, tenantID uuid.UUID) ([]Dataset, error) {
	rows, err := q.Query(ctx, `
		SELECT d.id, d.name, d.notes, COALESCE(d.builtin_key, ''),
		       count(DISTINCT qs.id)::int,
		       count(DISTINCT t.topic_key)::int,
		       d.created_at, d.updated_at
		  FROM app_trivia_datasets d
		  LEFT JOIN app_trivia_questions qs
		         ON qs.dataset_id = d.id AND qs.tenant_id = d.tenant_id
		  LEFT JOIN app_trivia_question_topics t
		         ON t.question_id = qs.id AND t.tenant_id = d.tenant_id
		 WHERE d.tenant_id = $1
		 GROUP BY d.id
		 ORDER BY d.name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing trivia datasets: %w", err)
	}
	defer rows.Close()
	out := []Dataset{}
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.Notes, &d.BuiltinKey,
			&d.Questions, &d.Topics, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning trivia dataset: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpsertDataset creates a dataset or returns the existing one with that name,
// so re-uploading "Christmas 2026" replaces its contents rather than making a
// second dataset with the same label.
func UpsertDataset(ctx context.Context, db Querier, tenantID uuid.UUID, name, notes, builtinKey string) (uuid.UUID, error) {
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		INSERT INTO app_trivia_datasets (tenant_id, name, name_key, notes, builtin_key)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (tenant_id, name_key) DO UPDATE
		   SET name = EXCLUDED.name, notes = EXCLUDED.notes, updated_at = now()
		RETURNING id`, tenantID, name, FoldKey(name), notes, builtinKey).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upserting trivia dataset: %w", err)
	}
	return id, nil
}

// GetDataset loads one dataset.
func GetDataset(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Dataset, error) {
	sets, err := ListDatasets(ctx, pool, tenantID)
	if err != nil {
		return nil, err
	}
	for i := range sets {
		if sets[i].ID == id {
			return &sets[i], nil
		}
	}
	return nil, ErrNotFound
}

// ClearDataset removes a dataset's questions without removing the dataset, so
// a re-upload replaces the contents rather than merging into them. A question
// that is on some game's board blocks it (ON DELETE RESTRICT), which is the
// same protection deleting a single question has.
func ClearDataset(ctx context.Context, tx pgx.Tx, tenantID, datasetID uuid.UUID) error {
	inUse, err := DatasetInUse(ctx, tx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if inUse != "" {
		return fmt.Errorf("%w: %s", ErrDatasetInUse, inUse)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM app_trivia_questions WHERE tenant_id = $1 AND dataset_id = $2`,
		tenantID, datasetID); err != nil {
		return fmt.Errorf("clearing trivia dataset: %w", err)
	}
	return nil
}

// DeleteDataset removes a dataset and its questions.
func DeleteDataset(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	inUse, err := DatasetInUse(ctx, pool, tenantID, id)
	if err != nil {
		return err
	}
	if inUse != "" {
		return fmt.Errorf("%w: %s", ErrDatasetInUse, inUse)
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM app_trivia_datasets WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting trivia dataset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RenameDataset changes the label and notes.
func RenameDataset(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, name, notes string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE app_trivia_datasets SET name = $3, name_key = $4, notes = $5, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2`,
		tenantID, id, name, FoldKey(name), notes)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: a dataset called %q already exists", ErrBadRequest, name)
		}
		return fmt.Errorf("renaming trivia dataset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GameDatasetIDs returns the datasets a game draws from.
//
// AN EMPTY RESULT MEANS EVERY DATASET, and callers must treat it that way. A
// game created before its datasets existed, or one whose only selected
// dataset was later deleted, has to stay playable rather than becoming a game
// that can never build a board.
func GameDatasetIDs(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx,
		`SELECT dataset_id FROM app_trivia_game_datasets WHERE tenant_id = $1 AND game_id = $2`,
		tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing game datasets: %w", err)
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning game dataset: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetGameDatasets replaces a game's selection. An empty slice clears it,
// which means "every dataset" — see GameDatasetIDs.
func SetGameDatasets(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, ids []uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning dataset selection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM app_trivia_game_datasets WHERE tenant_id = $1 AND game_id = $2`,
		tenantID, gameID); err != nil {
		return fmt.Errorf("clearing dataset selection: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_trivia_game_datasets (tenant_id, game_id, dataset_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tenantID, gameID, id); err != nil {
			return fmt.Errorf("selecting dataset: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE app_trivia_games SET state_version = state_version + 1, updated_at = now()
		  WHERE tenant_id = $1 AND id = $2`, tenantID, gameID); err != nil {
		return fmt.Errorf("bumping state version: %w", err)
	}
	return tx.Commit(ctx)
}
