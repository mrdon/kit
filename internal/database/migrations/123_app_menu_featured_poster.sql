-- +goose Up

-- The featured panel is now a full-size poster rather than a call to action,
-- and the board refuses an events:featured source on any other kind. Turn the
-- stored cta panels into poster panels so no board stops parsing on deploy.
UPDATE app_menu_boards
SET payload = jsonb_set(payload, '{panels}', (
        SELECT jsonb_agg(
            CASE WHEN p->>'source' = 'events:featured'
                 THEN jsonb_build_object('kind', 'poster', 'label', p->'label', 'source', 'events:featured')
                 ELSE p END
            ORDER BY i)
        FROM jsonb_array_elements(payload->'panels') WITH ORDINALITY AS a(p, i)
    )),
    updated_at = NOW()
WHERE jsonb_typeof(payload->'panels') = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(payload->'panels') AS a(p)
              WHERE p->>'source' = 'events:featured' AND p->>'kind' <> 'poster');

-- +goose Down
SELECT 1;
