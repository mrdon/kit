-- +goose Up

-- A short join code, so the QR on the wall is easier to scan.
--
-- The QR is the only thing on that screen anybody has to act on, and it is
-- acted on from across a room by a phone camera in bar lighting. What decides
-- whether that works is the MODULE SIZE -- how many physical millimetres each
-- black square gets -- which is the panel size divided by the number of
-- modules. Fewer characters in the URL, fewer modules, bigger squares.
--
-- Measured, because the obvious fix does not work: shortening the path from
-- /trivia/ to /t/ changes nothing, since the encoder is picking a version
-- band and the URL stays inside the same one. What actually moves it:
--
--   https://kit.twdata.org/gravity-brewing/trivia/knitting-lemur   33 modules
--   https://kit.twdata.org/j/k7m2q                                 29 modules
--   ...and at error-correction level L                             25 modules
--
-- 33 -> 25 is a 21% bigger square at the same panel size.
--
-- The code is GLOBAL rather than per tenant, because the whole point is to
-- drop the workspace slug out of the URL -- it is the longest part.
-- Five base32 characters is about a million codes; the unique index plus a
-- retry is what guarantees no collision.
--
-- This does not replace the readable URL. /{slug}/trivia/{name} keeps
-- working, permanently: it is on whiteboards and in the address bar of every
-- TV already pointed at a game.
ALTER TABLE app_trivia_games ADD COLUMN join_code TEXT;

-- Backfill. Crockford-ish base32 without the letters that get misread aloud
-- or mistyped (i, l, o, u), generated per row.
UPDATE app_trivia_games
   SET join_code = (
       SELECT string_agg(substr('0123456789abcdefghjkmnpqrstvwxyz',
                                (floor(random() * 32) + 1)::int, 1), '')
         FROM generate_series(1, 5)
   )
 WHERE join_code IS NULL;

-- Any collision the backfill happened to create gets a second roll. At five
-- characters over the handful of rows this runs against, this is belt and
-- braces rather than something expected to fire.
UPDATE app_trivia_games g
   SET join_code = g.join_code || substr(md5(g.id::text), 1, 2)
 WHERE EXISTS (
     SELECT 1 FROM app_trivia_games o
      WHERE o.join_code = g.join_code AND o.id <> g.id
 );

CREATE UNIQUE INDEX idx_app_trivia_games_join_code
    ON app_trivia_games (join_code) WHERE join_code IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_app_trivia_games_join_code;
ALTER TABLE app_trivia_games DROP COLUMN IF EXISTS join_code;
