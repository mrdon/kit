-- +goose Up

-- Collapse menu boards to one per workspace.
--
-- The key was there so a workspace could run a second, different board on
-- another screen. Nobody wanted that, and it cost more than it looked: two
-- addresses to keep straight, a key in every tool call, a console listing
-- for a list that was always one long, and a way to create a board by
-- accident that then had to be deleted. A workspace has a menu.
--
-- Any extra boards are dropped. They were only ever reachable at an address
-- someone had to have been given, and the workspace menu -- the one at
-- /{slug}/menu, the one screens are pointed at -- is the row that survives.
DELETE FROM app_menu_boards WHERE key <> 'default';

DROP INDEX IF EXISTS idx_app_menu_boards_key;
DROP INDEX IF EXISTS idx_app_menu_boards_source;

ALTER TABLE app_menu_boards DROP COLUMN key;

-- One menu per workspace, enforced rather than assumed.
CREATE UNIQUE INDEX idx_app_menu_boards_tenant ON app_menu_boards (tenant_id);

-- +goose Down
DROP INDEX IF EXISTS idx_app_menu_boards_tenant;
ALTER TABLE app_menu_boards ADD COLUMN key TEXT NOT NULL DEFAULT 'default';
CREATE UNIQUE INDEX idx_app_menu_boards_key ON app_menu_boards (tenant_id, key);
CREATE INDEX idx_app_menu_boards_source ON app_menu_boards (tenant_id) WHERE source_kind <> '';
