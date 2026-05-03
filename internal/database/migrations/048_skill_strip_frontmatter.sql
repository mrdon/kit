-- +goose Up

-- Some skills were created via a path that stored the full SKILL.md
-- (including the leading `---\nname: ...\ndescription: ...\n---\n\n`
-- frontmatter) into skills.content. Skill.ToSKILLMD then prepends a
-- fresh frontmatter block on every load, doubling the YAML in the
-- agent's view and burning ~50-150 bytes per load_skill call. The
-- contract is that name and description live in their own columns and
-- content is body-only. Strip the leading frontmatter from any existing
-- row that has it.
--
-- Match: content begins with `---\n`, then any chars (possibly multi-
-- line) up to the first `\n---\n` closer. Capture everything after,
-- skipping any blank lines that follow the closer. Idempotent: rows
-- without leading frontmatter are untouched (regexp_replace returns the
-- input unchanged when the pattern doesn't match).

UPDATE skills
   SET content = regexp_replace(
                   content,
                   '^---\n.*?\n---\n\s*',
                   '',
                   'ns'
                 )
 WHERE content LIKE E'---\n%';

-- +goose Down

-- No down: restoring the embedded frontmatter would require knowing the
-- original (potentially edited) name/description from before the strip.
-- The forward migration is loss-free relative to the contract (body
-- preserved, frontmatter regenerated on read by ToSKILLMD).
SELECT 1;
