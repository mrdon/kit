package trivia

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The one import path. The console upload, the starter packs and the
// trivia_import_pack tool all write through importQuestions, so a sheet
// lands the same way whichever door it came in by.

// importTarget names the dataset an import writes into.
type importTarget struct {
	Name       string
	Notes      string
	BuiltinKey string
	// Replace empties the dataset first, so an upload is the dataset's new
	// contents rather than an addition to them.
	Replace bool
}

// importResponse is what the host sees after an upload.
//
// The TOPIC HISTOGRAM comes back from this same call, deliberately. It is
// what the setup page needs to offer column choices, and a host who uploads a
// sheet and is told only "38 imported" has no idea what they got or whether
// any column can fill a board.
type importResponse struct {
	DatasetID         string       `json:"dataset_id"`
	Imported          int          `json:"imported"`
	Updated           int          `json:"updated"`
	SkippedDuplicates int          `json:"skipped_duplicates"`
	Errors            []RowError   `json:"errors"`
	Truncated         bool         `json:"truncated"`
	Topics            []TopicCount `json:"topics"`
}

// importQuestions writes the good rows and reports the bad. A three-hundred-row
// sheet with two typos is not a total failure.
func importQuestions(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, plan ImportPlan, opts importTarget) (importResponse, error) {
	resp := importResponse{
		Errors:            plan.Errors,
		Truncated:         plan.Truncated,
		SkippedDuplicates: plan.Skipped,
	}
	if resp.Errors == nil {
		resp.Errors = []RowError{}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return resp, fmt.Errorf("beginning import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	datasetID, err := UpsertDataset(ctx, tx, tenantID, opts.Name, opts.Notes, opts.BuiltinKey)
	if err != nil {
		return resp, err
	}
	// A re-upload REPLACES the dataset's contents rather than merging into
	// them. Merging would make a removed question impossible to remove: you
	// would fix your sheet, upload it, and the stale row would still be
	// there.
	if opts.Replace {
		if err := ClearDataset(ctx, tx, tenantID, datasetID); err != nil {
			return resp, err
		}
	}

	for _, q := range plan.Rows {
		_, inserted, err := UpsertQuestion(ctx, tx, tenantID, datasetID, q)
		if err != nil {
			return resp, err
		}
		// Re-uploading a corrected sheet updates in place rather than
		// duplicating, and the host is told which happened.
		if inserted {
			resp.Imported++
		} else {
			resp.Updated++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return resp, fmt.Errorf("committing import: %w", err)
	}

	resp.DatasetID = datasetID.String()
	hist, err := TopicHistogram(ctx, pool, tenantID, []uuid.UUID{datasetID}, Freshness{})
	if err != nil {
		return resp, err
	}
	resp.Topics = hist
	if resp.Topics == nil {
		resp.Topics = []TopicCount{}
	}
	return resp, nil
}
