package posters

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/posterrender"
)

// The seven layouts the prototype proved ship with Kit as built-in
// templates. Their TSX lives in builtins/ and is also what the renderer's
// own tests exercise, so a change here is checked on three canvases before
// it lands.

//go:embed builtins/*.tsx
var builtinFS embed.FS

// BuiltinTemplate is one shipped layout.
type BuiltinTemplate struct {
	Key    string
	Source string
	Meta   posterrender.TemplateMeta
}

// builtinOrder is the spread order: most distinct first, so a short list
// of options still covers the range.
var builtinOrder = []string{"split", "when", "full", "band", "inset", "strip", "type"}

// Builtins reads the embedded templates in spread order.
func Builtins() ([]BuiltinTemplate, error) {
	entries, err := fs.ReadDir(builtinFS, "builtins")
	if err != nil {
		return nil, fmt.Errorf("reading built-in templates: %w", err)
	}
	byKey := map[string]BuiltinTemplate{}
	for _, e := range entries {
		key := strings.TrimSuffix(e.Name(), ".tsx")
		raw, err := builtinFS.ReadFile("builtins/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading built-in template %s: %w", key, err)
		}
		meta, err := metaFromSource(string(raw))
		if err != nil {
			return nil, fmt.Errorf("built-in template %s: %w", key, err)
		}
		byKey[key] = BuiltinTemplate{Key: key, Source: string(raw), Meta: meta}
	}
	out := make([]BuiltinTemplate, 0, len(byKey))
	for _, k := range builtinOrder {
		if t, ok := byKey[k]; ok {
			out = append(out, t)
			delete(byKey, k)
		}
	}
	rest := make([]string, 0, len(byKey))
	for k := range byKey {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, byKey[k])
	}
	return out, nil
}

var (
	metaName  = regexp.MustCompile(`name:\s*"([^"]*)"`)
	metaDesc  = regexp.MustCompile(`description:\s*"([^"]*)"`)
	metaMin   = regexp.MustCompile(`min:\s*(\d+)`)
	metaMax   = regexp.MustCompile(`max:\s*(\d+)`)
	metaNeeds = regexp.MustCompile(`needs:\s*\[([^\]]*)\]`)
	metaBlock = regexp.MustCompile(`(?s)export const meta\s*=\s*\{(.*?)\n\};`)
)

// metaFromSource reads the meta export of a template whose meta is a plain
// literal, which is how built-ins are written. Tenant templates get theirs
// from the renderer, which runs the code.
func metaFromSource(src string) (posterrender.TemplateMeta, error) {
	var m posterrender.TemplateMeta
	block := metaBlock.FindStringSubmatch(src)
	if block == nil {
		return m, errors.New("no `export const meta = {...};` block")
	}
	body := block[1]
	if x := metaName.FindStringSubmatch(body); x != nil {
		m.Name = x[1]
	}
	if x := metaDesc.FindStringSubmatch(body); x != nil {
		m.Description = x[1]
	}
	if x := metaMin.FindStringSubmatch(body); x != nil {
		m.Photos.Min, _ = strconv.Atoi(x[1])
	}
	if x := metaMax.FindStringSubmatch(body); x != nil {
		m.Photos.Max, _ = strconv.Atoi(x[1])
	}
	m.Needs = []string{}
	if x := metaNeeds.FindStringSubmatch(body); x != nil {
		for n := range strings.SplitSeq(x[1], ",") {
			if n = strings.Trim(strings.TrimSpace(n), `"`); n != "" {
				m.Needs = append(m.Needs, n)
			}
		}
	}
	if m.Name == "" {
		return m, errors.New("meta has no name")
	}
	return m, nil
}

// installBuiltins converges the shared built-in rows with the embedded
// sources. Runs at startup; a changed source gets a new version.
func (a *App) installBuiltins(ctx context.Context) {
	ts, err := Builtins()
	if err != nil {
		slog.Error("posters: reading built-in templates", "error", err)
		return
	}
	for _, t := range ts {
		if err := upsertBuiltin(ctx, a.pool, t.Key, t.Meta, t.Source); err != nil {
			slog.Error("posters: installing built-in template", "key", t.Key, "error", err)
		}
	}
}
