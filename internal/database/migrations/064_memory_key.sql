-- +goose Up

-- Optional dedup key for memories. When set, a memory is a single mutable
-- value per (scope_id, key) rather than an append-only fact: save_memory with
-- a key upserts in place. Lets scheduled skills keep a deterministic cursor
-- (e.g. an email-sweep watermark) without the forget-then-save dance, and
-- without polluting the human-readable content with a synthetic id.
--
-- Nullable on purpose: the keyless path stays pure-append, so existing
-- "remember this fact" callers are unaffected. The partial unique index only
-- constrains keyed rows, leaving keyless rows free to accumulate.
ALTER TABLE memories ADD COLUMN key TEXT;

CREATE UNIQUE INDEX memories_scope_key_idx
  ON memories (scope_id, key) WHERE key IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS memories_scope_key_idx;
ALTER TABLE memories DROP COLUMN key;
