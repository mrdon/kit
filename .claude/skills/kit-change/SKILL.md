---
name: kit-change
description: Make a change to a Kit app (COGS, menu, trivia, events...) and send it to Don for review as a pull request, without touching main or deploying. Use when someone other than Don wants to change, fix or improve a Kit app, or says "make a change", "fix this in kit", "ship this to Don", "send this for review", or similar.
---

# Make a Kit change for review

The person running this is changing Kit but is not the one who deploys it.
Their work goes on a branch and into a pull request. Don reviews it, merges
it and deploys. **Never push to `main`, never push to the `dokku` remote, and
never merge the pull request**, even if asked: say that Don does those steps.

They may know the business well (brewing, the taproom) and not know Go or
React. Talk in their terms, not code terms.

## 1. Start from a clean branch

```bash
git status --short
git branch --show-current
```

Name branches `<name>/<short-topic>`, where `<name>` is the first word of
`git config user.name`, lowercased (e.g. `sam/tank-fill-numbers`).

- On `main` with no local changes: fetch and branch off the latest main.
  ```bash
  git fetch origin
  git switch -c <name>/<short-topic> origin/main
  ```
- On a branch named `worktree-...`: Claude Code made a fresh worktree for
  this session. Treat it like `main`: run the two commands above.
- On another non-main branch: this is probably a change in progress. Ask
  whether to keep going on it or start fresh.
- Uncommitted changes on `main`: don't discard them. Ask what they are, then
  move them onto a new branch (`git switch -c ...` keeps them).

## 2. Understand the change before coding

- Find the app being changed and read its `CLAUDE.md`
  (e.g. `internal/apps/cogs/CLAUDE.md`). If it has none, read the app's
  `app.go` and say so: the change will be slower and riskier.
- Restate the change in plain words: what the person will see differently,
  and which screens or numbers it affects. Say if it touches any of the
  app's rules or "What needs Don" items.
- Get a yes before writing code. If it needs Don, say so and offer to write
  it up as a note for him instead of building it.

## 3. Make the change

- Keep it inside the app's files. If you find you need to change shared Kit
  code, stop and explain why: that part goes to Don.
- Add or update a test that does what the person described and checks the
  result. Tests are Go only: for a screen change, put the logic (the label,
  the number, the rule) in the Go view code and test it there, and keep the
  React side to displaying it.
- To see it, run `make serve` (after `make dev-setup` once) and open the
  links `make dev-setup` printed. Restart `make serve` after each change.

## 4. Check it

```bash
make up
make prepush
```

Fix failures in the code you changed. If something fails in code you didn't
touch, stop: tell the person it looks like an existing problem and don't
try to fix it.

Then check how far the change reached. Use three dots: this lists only what
this branch changed, not what landed on main since it started.

```bash
git fetch origin
git status --short
git diff --stat origin/main...HEAD
```

- Anything outside the app's folder, its migrations, its frontend pages and
  its API client should be explained in the pull request.
- If `web/package-lock.json` shows up and the change didn't add a package,
  it's noise from a build: `git checkout web/package-lock.json` and don't
  commit it.

## 5. Send it to Don

Commit with a short message saying what changed, in plain words. Then:

```bash
git push -u origin HEAD
gh pr create --base main --title "<what changed, plain words>" --body-file <file>
```

Write the pull request body for Don, kept short:

- **What:** the change, from the user's side.
- **Why:** what the person asked for and the problem it solves.
- **How to check:** the screen and steps to see it working.
- **Outside the app:** any shared files touched and why, or "none".
- **Database:** any new migration file, or "none".

If `git push` or `gh` is refused for permissions, stop and tell the person
to ask Don for access. Don't work around it.

## 6. Tell them what happens next

Give them the pull request link. Explain: tests run automatically on GitHub,
Don reviews it, and once he merges and deploys, it's live. If Don asks for
changes, they can run this skill again on the same branch.
