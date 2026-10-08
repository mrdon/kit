-- +goose Up

-- A device's start link. A kiosk-mode browser boots to one fixed URL and
-- wipes its cookies on every restart, so a pairing it holds in a cookie is
-- gone by morning. The start link carries a secret that, opened, signs the
-- browser in as this device for the session. Only the hash is stored; the
-- link is shown once when made, and making a new one invalidates the old.
ALTER TABLE actors ADD COLUMN start_token_hash TEXT UNIQUE;

-- +goose Down
ALTER TABLE actors DROP COLUMN IF EXISTS start_token_hash;
