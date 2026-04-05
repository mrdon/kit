-- +goose Up
-- Delete duplicate skills keeping the latest one per tenant+name
-- +goose StatementBegin
DO $$
BEGIN
    DELETE FROM skills a USING skills b
    WHERE a.tenant_id = b.tenant_id
      AND a.name = b.name
      AND a.created_at < b.created_at;
END $$;
-- +goose StatementEnd

ALTER TABLE skills ADD CONSTRAINT skills_tenant_name_unique UNIQUE (tenant_id, name);

-- +goose Down
ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_tenant_name_unique;
