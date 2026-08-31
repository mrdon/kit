-- Seed a local workspace with a menu board that follows a real Untappd board,
-- so the whole path -- fetch, parse, store, render -- can be exercised without
-- deploying.
--
--   make up
--   ./dist/kit &
--   docker compose exec -T postgres psql -U kit -d kit \
--     < internal/apps/menu/testdata/seed_local.sql
--   open http://localhost:8099/local/menu
--
-- The first request pulls from Untappd for real, so the board that renders is
-- the live tap list. Re-request after 60s to see it refresh.
--
-- Idempotent: run it as often as you like.

INSERT INTO tenants (id, slack_team_id, name, bot_token, slug, timezone, setup_complete)
VALUES (gen_random_uuid(), 'T_LOCAL_MENU', 'Local Dev', '', 'local', 'America/Denver', true)
ON CONFLICT (slack_team_id) DO UPDATE SET slug = EXCLUDED.slug;

-- Presentation only. The taps below are a single placeholder so the payload
-- validates; the first request replaces them with whatever Untappd has.
INSERT INTO app_menu_boards (tenant_id, name, payload, source_kind, source_id)
SELECT
    t.id,
    'Menu',
    $json${
      "venue": {
        "wordmark": "Gravity Brewing",
        "footer": [
          "Pours are 16oz unless marked",
          "Also in 4oz & 9oz",
          "Crowlers, growlers & cans to go"
        ]
      },
      "taps": [
        {"section": "Loading", "name": "Pulling from Untappd", "style": "",
         "abv": "", "price": "0", "size": "16oz"}
      ],
      "panels": [
        {"kind": "agenda", "label": "This week", "image": "asset:this-week",
         "events": [
           {"when": "Every Wed", "time": "6:30 pm", "title": "Trivia",
            "note": "With Geeks Who Drink"},
           {"when": "Fri 11 Sep", "time": "6:00 pm", "title": "Bike Night",
            "note": "Ride in, park out front"}
         ]},
        {"kind": "poster", "label": "Don't miss", "image": "asset:anniversary",
         "alt": "14th Anniversary Celebration"},
        {"kind": "cta", "label": "Book the space", "image": "asset:party",
         "headline": "Your party, here.",
         "body": "Birthdays, team nights, wedding parties and wakes.",
         "contact": ["info@thegravitybrewing.com", "(303) 544-0746"]}
      ]
    }$json$::jsonb,
    'untappd',
    '22128'
FROM tenants t
WHERE t.slack_team_id = 'T_LOCAL_MENU'
ON CONFLICT (tenant_id) DO UPDATE
    SET source_kind = EXCLUDED.source_kind,
        source_id   = EXCLUDED.source_id,
        -- Clear the hash so the next request pulls rather than trusting a
        -- stale copy from an earlier run.
        source_hash = '',
        synced_at   = NULL;
