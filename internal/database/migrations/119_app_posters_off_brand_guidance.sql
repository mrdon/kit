-- +goose Up
-- A version a person asked to break the brand guide for (a gradient, a
-- fade): its brand findings are warnings, so it re-renders at any format.
ALTER TABLE app_poster_versions ADD COLUMN off_brand BOOLEAN NOT NULL DEFAULT false;
-- Admin-written guidance added to the poster and template chat prompts.
ALTER TABLE app_poster_settings ADD COLUMN guidance TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_poster_settings DROP COLUMN guidance;
ALTER TABLE app_poster_versions DROP COLUMN off_brand;
