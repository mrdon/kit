-- +goose Up
ALTER TABLE tasks ADD COLUMN skill_id uuid REFERENCES skills(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE tasks DROP COLUMN IF EXISTS skill_id;
