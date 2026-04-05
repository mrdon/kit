package skills

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/mrdon/kit/internal/skills/builtins"
)

// BuiltinSkill represents a skill embedded in the binary.
type BuiltinSkill struct {
	Name        string
	Description string
	Content     string // full markdown body (after frontmatter)
}

// builtinCache is populated once at init time.
var builtinCache []BuiltinSkill

func init() {
	builtinCache = discover(builtins.FS)
}

// Builtins returns all built-in skills.
func Builtins() []BuiltinSkill {
	return builtinCache
}

// GetBuiltin returns a built-in skill by name, or nil if not found.
func GetBuiltin(name string) *BuiltinSkill {
	for i := range builtinCache {
		if builtinCache[i].Name == name {
			return &builtinCache[i]
		}
	}
	return nil
}

// MatchBuiltins returns built-in skills whose name or description
// contains the search string (case-insensitive).
func MatchBuiltins(search string) []BuiltinSkill {
	if search == "" {
		return builtinCache
	}
	lower := strings.ToLower(search)
	var matches []BuiltinSkill
	for _, s := range builtinCache {
		if strings.Contains(strings.ToLower(s.Name), lower) ||
			strings.Contains(strings.ToLower(s.Description), lower) {
			matches = append(matches, s)
		}
	}
	return matches
}

// discover scans an embedded FS for directories containing SKILL.md.
func discover(fsys fs.FS) []BuiltinSkill {
	var skills []BuiltinSkill
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name()+"/SKILL.md")
		if err != nil {
			continue
		}
		s, err := parseSkillMD(string(data))
		if err != nil {
			continue
		}
		if s.Name == "" {
			s.Name = e.Name()
		}
		skills = append(skills, s)
	}
	return skills
}

// parseSkillMD parses a SKILL.md file with YAML-like frontmatter.
func parseSkillMD(raw string) (BuiltinSkill, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return BuiltinSkill{}, errors.New("missing frontmatter")
	}
	rest := raw[3:]
	before, after, found := strings.Cut(rest, "\n---")
	if !found {
		return BuiltinSkill{}, errors.New("unclosed frontmatter")
	}
	frontmatter := before
	body := strings.TrimSpace(after)

	var s BuiltinSkill
	for line := range strings.SplitSeq(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		val = strings.Trim(val, "\"")
		switch strings.TrimSpace(key) {
		case "name":
			s.Name = val
		case "description":
			s.Description = val
		}
	}
	s.Content = body
	return s, nil
}
