-- +goose Up
-- +goose StatementBegin

-- resolutions holds 0-3 tappable actions suggested by Haiku at todo-create
-- time. NULL = not yet generated; [] = generated but nothing fit. Each item
-- has {id, label, prompt, shape, cron?}; see internal/apps/todo/resolutions.go.
ALTER TABLE app_todos ADD COLUMN resolutions JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE app_todos DROP COLUMN IF EXISTS resolutions;

-- +goose StatementEnd
