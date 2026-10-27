-- +goose Up

-- Panels pinned to one event by an `event` id are gone: a featured night now
-- gets the board's `events:featured` panel instead. The board rejects keys it
-- does not know, so a pinned panel left in a payload would take the whole
-- board down. Drop those panels rather than strip the key, which would leave
-- their dated text on the wall with nothing to take it down.
UPDATE app_menu_boards
SET payload = jsonb_set(payload, '{panels}', (
        SELECT COALESCE(jsonb_agg(p ORDER BY i), '[]'::jsonb)
        FROM jsonb_array_elements(payload->'panels') WITH ORDINALITY AS a(p, i)
        WHERE NOT p ? 'event'
    )),
    updated_at = NOW()
WHERE jsonb_typeof(payload->'panels') = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(payload->'panels') AS a(p) WHERE p ? 'event');

-- +goose Down
SELECT 1;
