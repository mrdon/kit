-- +goose Up

-- A how-to-play video for the lobby screen, per workspace. It is a Google
-- Drive link, shared as "anyone with the link", that Kit fetches and caches
-- and the TV plays beside the join code while people wait. Empty means no
-- video and the lobby is the plain join screen, which is where every
-- workspace starts. The video itself is never stored here: Drive is the copy
-- the venue edits, and replacing the file there (Manage versions) is how it
-- is updated.
ALTER TABLE app_trivia_settings
    ADD COLUMN explainer_video_link TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE app_trivia_settings DROP COLUMN explainer_video_link;
