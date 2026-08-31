package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// SourceUntappd is the only upstream this app knows how to read.
const SourceUntappd = "untappd"

// SyncResult is what one pull did, for the tool output and the logs.
type SyncResult struct {
	Key     string
	Taps    int
	Changed bool
	Err     error
}

// FreshFor is how long a pulled tap list is trusted before the next request
// triggers another look upstream.
//
// Sixty seconds is a compromise between two costs that only exist while
// someone is looking. A keg blowing is noticed by whoever is pouring, so a
// screen a minute behind is indistinguishable from live to a customer; and a
// minute is long enough that a wall of screens all polling cannot turn into a
// wall of requests at Untappd.
const FreshFor = 60 * time.Second

// refreshTimeout bounds how long a page render will wait on Untappd. Past
// this the stored tap list is served instead. A board a few minutes stale is
// a small problem; a blank screen in a full taproom is not.
const refreshTimeout = 8 * time.Second

// pulls coalesces concurrent refreshes. Several screens plus their version
// polls can land in the same second, and without this each would open its own
// request to Untappd for the identical answer.
var pulls singleflight.Group

// EnsureFresh returns the board, pulling from upstream first when the stored
// copy has gone stale.
//
// This is deliberately lazy rather than scheduled. The screens are switched
// off overnight, so a cron would spend most of its runs fetching a menu
// nobody is looking at — and the moment a screen does come on, a schedule is
// exactly the wrong thing, because the answer is up to a full interval old.
// Fetching when asked makes the cost track the watching.
//
// A refresh failure is never fatal: the stored board is returned and the
// error recorded, so an Untappd outage makes the wall go stale rather than
// dark.
func (a *App) EnsureFresh(ctx context.Context, tenantID uuid.UUID, row *BoardRow) *BoardRow {
	if row == nil || row.SourceKind == "" {
		return row // hand-set tap list: nothing upstream to ask
	}
	if row.SyncedAt != nil && time.Since(*row.SyncedAt) < FreshFor {
		return row
	}

	key := tenantID.String() + "/" + row.Key
	fresh, _, _ := pulls.Do(key, func() (any, error) {
		// Detached from the caller's deadline so one abandoned request does
		// not cancel a pull the next caller is about to wait on.
		pullCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()

		if res := a.syncBoard(pullCtx, tenantID, row); res.Err != nil {
			slog.Warn("menu refresh failed, serving stored board",
				"tenant_id", tenantID, "key", row.Key, "error", res.Err)
			return row, nil
		}
		updated, err := GetBoardByKey(pullCtx, a.pool, tenantID, row.Key)
		if err != nil || updated == nil {
			return row, nil
		}
		return updated, nil
	})

	if b, ok := fresh.(*BoardRow); ok && b != nil {
		return b
	}
	return row
}

// SyncTenant pulls every sourced board for a workspace.
func (a *App) SyncTenant(ctx context.Context, tenantID uuid.UUID) ([]SyncResult, error) {
	boards, err := ListSourcedBoards(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]SyncResult, 0, len(boards))
	for _, row := range boards {
		out = append(out, a.syncBoard(ctx, tenantID, row))
	}
	return out, nil
}

// syncBoard pulls one board's tap list and merges it into the stored payload.
//
// Merge, not replace: only `taps` has an upstream. The wordmark, the footer
// rules and the rotating panels are presentation with no source to restore
// them from, so a sync that overwrote the whole document would silently
// delete work on every refresh.
func (a *App) syncBoard(ctx context.Context, tenantID uuid.UUID, row *BoardRow) SyncResult {
	res := SyncResult{Key: row.Key}

	if row.SourceKind != SourceUntappd {
		res.Err = fmt.Errorf("unknown source kind %q", row.SourceKind)
		a.stampError(ctx, tenantID, row.Key, res.Err)
		return res
	}

	body, hash, err := FetchUntappdBody(ctx, untappdClient(), row.SourceID)
	if err != nil {
		res.Err = err
		a.stampError(ctx, tenantID, row.Key, err)
		return res
	}

	// The cheap exit, and the reason a one-minute schedule is reasonable.
	if hash == row.SourceHash {
		if err := TouchSynced(ctx, a.pool, tenantID, row.Key, hash); err != nil {
			slog.Warn("touching menu sync", "tenant_id", tenantID, "key", row.Key, "error", err)
		}
		return res
	}

	taps := ParseUntappdBoard(body)
	if len(taps) < minPlausibleTaps {
		res.Err = fmt.Errorf("%w: got %d from board %s",
			ErrScrapeImplausible, len(taps), row.SourceID)
		a.stampError(ctx, tenantID, row.Key, res.Err)
		return res
	}
	res.Taps = len(taps)

	board, err := ParseBoard(row.Payload)
	if err != nil {
		// A board whose stored payload no longer parses still has a usable
		// upstream, so rebuild around the taps rather than refusing forever.
		slog.Warn("menu payload unparseable, rebuilding from source",
			"tenant_id", tenantID, "key", row.Key, "error", err)
		board = &Board{}
	}
	board.Taps = taps

	if err := board.Validate(); err != nil {
		res.Err = err
		a.stampError(ctx, tenantID, row.Key, err)
		return res
	}

	payload, err := json.Marshal(board)
	if err != nil {
		res.Err = fmt.Errorf("encoding synced board: %w", err)
		a.stampError(ctx, tenantID, row.Key, res.Err)
		return res
	}

	// The upstream page moved but the taps we care about did not — Untappd
	// re-renders with a new checkin count or timestamp constantly. Record the
	// new hash so the next tick exits cheaply, but leave updated_at alone so
	// it keeps meaning "when the tap list last actually changed".
	if bytes.Equal(normalizeJSON(row.Payload), normalizeJSON(payload)) {
		if err := TouchSynced(ctx, a.pool, tenantID, row.Key, hash); err != nil {
			slog.Warn("touching menu sync", "tenant_id", tenantID, "key", row.Key, "error", err)
		}
		return res
	}
	if err := SaveSyncedTaps(ctx, a.pool, tenantID, row.Key, payload, hash); err != nil {
		res.Err = err
		return res
	}
	res.Changed = true
	return res
}

func (a *App) stampError(ctx context.Context, tenantID uuid.UUID, key string, cause error) {
	if err := RecordSyncError(ctx, a.pool, tenantID, key, cause.Error()); err != nil {
		slog.Warn("recording menu sync error", "tenant_id", tenantID, "key", key, "error", err)
	}
}

// normalizeJSON re-encodes so a comparison is about content rather than key
// order or whitespace from whoever wrote the row last.
func normalizeJSON(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// SetSource points a board at an upstream and immediately pulls once, so
// configuring it is also the test that it works.
func (a *App) SetSource(ctx context.Context, tenantID uuid.UUID, key, kind, sourceID string) (*BoardRow, SyncResult, error) {
	if key == "" {
		key = DefaultKey
	}
	if kind != SourceUntappd && kind != "" {
		return nil, SyncResult{}, fmt.Errorf("unknown source %q (want %q)", kind, SourceUntappd)
	}
	row, err := SetBoardSource(ctx, a.pool, tenantID, key, kind, sourceID)
	if err != nil {
		return nil, SyncResult{}, err
	}
	if kind == "" {
		return row, SyncResult{Key: key}, nil
	}
	return row, a.syncBoard(ctx, tenantID, row), nil
}
