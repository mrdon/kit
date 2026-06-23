-- +goose Up

-- Single-owner model: a skill has exactly one scope, consistent with rules,
-- memories, jobs, and tasks (which already enforce a single scope). Historically
-- skill_scopes was a many-to-many and create_skill's upsert could accumulate
-- rows, so some skills carry several scopes. Collapse each such skill to ONE
-- row, keeping the broadest (most permissive) scope so effective visibility is
-- preserved — a skill that was tenant-wide (public) stays public; the union of
-- its scopes was already that broad.
--
-- Rank: tenant-wide (0) > the universal "member" catchall (1) > any other role
-- (2) > a user scope (3). scope_id breaks ties deterministically.

DELETE FROM skill_scopes ss
USING (
  SELECT ss2.tenant_id, ss2.skill_id, ss2.scope_id,
    ROW_NUMBER() OVER (
      PARTITION BY ss2.tenant_id, ss2.skill_id
      ORDER BY
        CASE
          WHEN sc.role_id IS NULL AND sc.user_id IS NULL THEN 0
          WHEN r.name = 'member' THEN 1
          WHEN sc.role_id IS NOT NULL THEN 2
          ELSE 3
        END,
        ss2.scope_id
    ) AS rn
  FROM skill_scopes ss2
  JOIN scopes sc ON sc.id = ss2.scope_id
  LEFT JOIN roles r ON r.id = sc.role_id
) ranked
WHERE ss.tenant_id = ranked.tenant_id
  AND ss.skill_id = ranked.skill_id
  AND ss.scope_id = ranked.scope_id
  AND ranked.rn > 1;

-- +goose Down

-- Irreversible: the dropped scope rows weren't recorded. Down is a no-op so the
-- migration can still be rolled back in the version table without error.
SELECT 1;
