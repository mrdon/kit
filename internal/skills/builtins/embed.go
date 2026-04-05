package builtins

import "embed"

// FS embeds all built-in skill directories. Adding a new built-in skill
// requires only creating a new subdirectory with a SKILL.md — no code changes.
//
//go:embed all:*
var FS embed.FS
