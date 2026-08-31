-- +goose Up

-- Give the info briefings already sitting in the stack the shelf life they
-- would have been created with today.
--
-- Expiry has existed since 013, but it was opt-in: a caller had to pass
-- ttl_days, and almost nothing did. The result is a feed carrying months of
-- "created 3 tasks" and "sync finished" notes that were interesting for an
-- afternoon and have been costing a swipe each ever since. The service layer
-- now defaults an info-severity briefing to a three-day shelf life
-- (cards.DefaultInfoBriefingTTL), and this backfill applies the same rule
-- retroactively so the fix isn't only felt on cards created from here on.
--
-- Three deliberate limits on what this touches:
--
--   * info only. notable and important are severities an author chose on
--     purpose, and the new default leaves those alone; so does this.
--   * expires_at IS NULL only. A card whose author named a deadline keeps it,
--     even if that deadline is further out than three days.
--   * pending only. Terminal cards are already out of the stack, and the
--     nightly retention purge owns their lifecycle.
--
-- created_at + 3 days, not now() + 3 days: the point is that these went stale
-- long ago. Almost every row lands in the past and the every-minute sweep
-- (cards.sweep) archives it on its next tick. Archived, not deleted — the
-- cards stay readable and the 90-day retention purge still owns the delete.
UPDATE app_cards c
SET expires_at = c.created_at + INTERVAL '3 days'
FROM app_card_briefings b
WHERE b.card_id = c.id
  AND c.kind = 'briefing'
  AND c.state = 'pending'
  AND c.expires_at IS NULL
  AND b.severity = 'info';

-- A second, narrower pass for the notable cards that are stale anyway.
--
-- The forward rule says notable waits until a human acks it, and that rule
-- stands. What it can't fix is history: the daily-recap job picked its own
-- severity each run and was inconsistent about it — Aug 23's recap is info,
-- Aug 24's is notable, and they are the same card written a day apart. The
-- severity-scaled backfill above would clear one and leave the other, which
-- is a worse outcome than either rule applied evenly.
--
-- So this clears notable briefings that are already two weeks old, on the
-- reasoning that a fortnight of sitting unacked is itself the evidence that
-- nobody was waiting on it. It is deliberately a one-off cleanup of what
-- exists today, not a policy: nothing in the service layer expires notable
-- cards, and one written tomorrow still waits for its human.
--
-- 'important' is left alone entirely. There are none pending, and the whole
-- point of the top severity is that it doesn't get swept up in tidying.
UPDATE app_cards c
SET expires_at = c.created_at + INTERVAL '14 days'
FROM app_card_briefings b
WHERE b.card_id = c.id
  AND c.kind = 'briefing'
  AND c.state = 'pending'
  AND c.expires_at IS NULL
  AND b.severity = 'notable'
  AND c.created_at < now() - INTERVAL '14 days';

-- +goose Down

-- Undoing this can only be approximate: the column is nullable and rows the
-- backfill skipped are indistinguishable afterwards from rows it never saw.
-- Clearing exactly the deadlines that match the formula is the closest
-- reversal available, and it errs toward leaving author-set deadlines intact
-- rather than wiping a real one.
--
-- Cards the sweep has already archived stay archived — this migration is not
-- the right place to resurrect them, and a state flip back to pending would
-- be a bigger lie than the deadline it is undoing.
UPDATE app_cards c
SET expires_at = NULL
FROM app_card_briefings b
WHERE b.card_id = c.id
  AND c.kind = 'briefing'
  AND ((b.severity = 'info' AND c.expires_at = c.created_at + INTERVAL '3 days')
       OR (b.severity = 'notable' AND c.expires_at = c.created_at + INTERVAL '14 days'));
