-- +goose Up

-- Collapse the 4-value priority enum (low/medium/high/urgent) into the three
-- the console's priority-band view actually uses: blocker > high > normal.
-- 'urgent' becomes 'blocker' (do-first / blocking other work), 'high' stays,
-- and low+medium fold into 'normal'. Data is remapped before the new CHECK
-- is added so existing rows don't violate it.
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_priority_check;

UPDATE app_tasks SET priority = CASE priority
  WHEN 'urgent' THEN 'blocker'
  WHEN 'high'   THEN 'high'
  ELSE 'normal'
END;

ALTER TABLE app_tasks ALTER COLUMN priority SET DEFAULT 'normal';
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_priority_check
  CHECK (priority IN ('blocker', 'high', 'normal'));

-- LLM-suggested topical category ("brewing", "sales", …). Distinct from the
-- task's role: the role is the visibility boundary (who can see the task);
-- the category is just a label for grouping work in the same area. Nullable —
-- filled asynchronously by the categorizer on create, sticky once set, and
-- overridable by the user.
ALTER TABLE app_tasks ADD COLUMN category TEXT;

-- +goose Down

ALTER TABLE app_tasks DROP COLUMN category;

ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_priority_check;
UPDATE app_tasks SET priority = CASE priority
  WHEN 'blocker' THEN 'urgent'
  WHEN 'high'    THEN 'high'
  ELSE 'medium'
END;
ALTER TABLE app_tasks ALTER COLUMN priority SET DEFAULT 'medium';
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_priority_check
  CHECK (priority IN ('low', 'medium', 'high', 'urgent'));
