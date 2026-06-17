-- +goose Up

-- `member` is a universal catchall: every user always holds it, and it's
-- resolved implicitly (GetUserRoleNames/GetUserRoleIDs UNION it in) rather
-- than stored. Older code auto-assigned it as a real user_roles row when a
-- user had no other roles; drop those rows so the table only ever holds
-- explicit, non-member assignments. Visibility is unchanged — member is
-- still effectively held by everyone.
DELETE FROM user_roles ur
USING roles r
WHERE ur.role_id = r.id
  AND r.name = 'member';

-- +goose Down

-- No-op: member rows were redundant (member is implicit), so there's nothing
-- meaningful to restore. Re-adding a row per user would reintroduce the very
-- state this migration removes.
