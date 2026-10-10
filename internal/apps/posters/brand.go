package posters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/posterrender"
)

// The brand comes from the tenant's branding-guide skill and nowhere else.
// A guide that carries a fenced ```json block of machine-readable tokens
// (color, approved_pairs, type, logo, canvas, constraints) is parsed
// directly; one that does not gets one Sonnet call per skill version to
// extract the same shape (brand_derive.go). Either way the result passes
// through deriveBrand, which turns tokens and roles into the grounds,
// accents and pairings the renderer validates against. Anything missing is
// reported, never guessed.

// guideTokens is the shape of the guide's JSON block (and of the model's
// extraction). Field names follow the Gravity guide, section 9.
type guideTokens struct {
	Color map[string]struct {
		Hex  string `json:"hex"`
		Role string `json:"role"`
	} `json:"color"`
	ApprovedPairs []struct {
		Fg        string  `json:"fg"`
		Bg        string  `json:"bg"`
		MinSizePx float64 `json:"min_size_px"`
	} `json:"approved_pairs"`
	Type struct {
		Display guideFont `json:"display"`
		Text    guideFont `json:"text"`
		Mono    guideFont `json:"mono"`
	} `json:"type"`
	Logo struct {
		Variants []string `json:"variants"`
		// on_<ground> keys arrive in the raw map below.
	} `json:"logo"`
	LogoRaw map[string]json.RawMessage `json:"-"`
	Canvas  map[string]struct {
		W          int `json:"w"`
		H          int `json:"h"`
		Safe       int `json:"safe"`
		SafeTop    int `json:"safe_top"`
		SafeBottom int `json:"safe_bottom"`
		SafeLeft   int `json:"safe_left"`
		SafeRight  int `json:"safe_right"`
	} `json:"canvas"`
	Constraints struct {
		MaxAccentElements     *int  `json:"max_amber_elements"`
		MaxAccent             *int  `json:"max_accent_elements"`
		AmberAndEmberTogether *bool `json:"amber_and_ember_together"`
		AccentsTogether       *bool `json:"accents_together"`
	} `json:"constraints"`
}

type guideFont struct {
	Family  string `json:"family"`
	Weight  int    `json:"weight"`
	Weights []int  `json:"weights"`
}

func (f guideFont) weights() []int {
	if len(f.Weights) > 0 {
		return f.Weights
	}
	if f.Weight > 0 {
		return []int{f.Weight}
	}
	return nil
}

var jsonFence = regexp.MustCompile("(?s)```json\\s*\\n(.*?)\\n\\s*```")

// guideJSONBlock finds the first fenced json block that looks like a
// token set. Returns "" when the guide has none.
func guideJSONBlock(content string) string {
	for _, m := range jsonFence.FindAllStringSubmatch(content, -1) {
		if strings.Contains(m[1], `"color"`) && strings.Contains(m[1], `"type"`) {
			return m[1]
		}
	}
	return ""
}

func parseGuideTokens(raw string) (*guideTokens, error) {
	var g guideTokens
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, fmt.Errorf("the json tokens block does not parse: %w", err)
	}
	var outer struct {
		Logo map[string]json.RawMessage `json:"logo"`
	}
	_ = json.Unmarshal([]byte(raw), &outer)
	g.LogoRaw = outer.Logo
	return &g, nil
}

// contentHash identifies a skill version for the brand cache.
func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// deriveBrand turns the guide's tokens into the renderer's brand. It
// returns the problems that stop a usable brand (missing tokens, no
// grounds, no fonts) and, when there are none, the brand.
func deriveBrand(g *guideTokens, hash string) (*posterrender.Brand, []string) {
	var problems []string
	b := &posterrender.Brand{Hash: hash, Tokens: map[string]string{}, Grounds: map[string]posterrender.Ground{}, Accents: map[string]posterrender.Accent{}, Formats: map[string]posterrender.Format{}, Logos: map[string]posterrender.ImageRef{}}
	roles := map[string]string{}
	for name, c := range g.Color {
		hexv := normalizeHex(c.Hex)
		if hexv == "" {
			problems = append(problems, fmt.Sprintf("color %q has no usable hex value", name))
			continue
		}
		b.Tokens[name] = hexv
		roles[name] = strings.ToLower(c.Role)
	}
	if len(b.Tokens) == 0 {
		problems = append(problems, "the guide names no color tokens")
	}
	for _, p := range g.ApprovedPairs {
		if _, ok := b.Tokens[p.Fg]; !ok {
			continue
		}
		if _, ok := b.Tokens[p.Bg]; !ok {
			continue
		}
		b.Pairs = append(b.Pairs, posterrender.Pair{Fg: p.Fg, Bg: p.Bg, MinSizePx: int(p.MinSizePx)})
	}
	if len(b.Pairs) == 0 && len(b.Tokens) > 0 {
		problems = append(problems, "the guide lists no approved foreground/background pairings")
	}
	problems = append(problems, deriveFonts(g, b)...)
	problems = append(problems, deriveAccents(g, roles, b)...)
	problems = append(problems, deriveGrounds(roles, readLogoMap(g), b)...)
	problems = append(problems, deriveFormats(g, b)...)
	if len(problems) > 0 {
		return nil, problems
	}
	return b, nil
}

func normalizeHex(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	if !regexp.MustCompile(`^#[0-9A-F]{6}$`).MatchString(s) {
		return ""
	}
	return s
}

func deriveFonts(g *guideTokens, b *posterrender.Brand) []string {
	var problems []string
	role := func(name string, f guideFont, fallback int) posterrender.FontRole {
		if strings.TrimSpace(f.Family) == "" {
			problems = append(problems, fmt.Sprintf("the guide names no %s typeface", name))
		}
		w := f.weights()
		if len(w) == 0 {
			w = []int{fallback}
		}
		sort.Ints(w)
		return posterrender.FontRole{Family: strings.TrimSpace(f.Family), Weights: w}
	}
	b.Fonts.Display = role("display", g.Type.Display, 700)
	b.Fonts.Text = role("text", g.Type.Text, 400)
	b.Fonts.Mono = role("mono (numbers)", g.Type.Mono, 500)
	// A condensed display face sets narrower than the default guess; the
	// safe-area check catches the rest.
	if strings.Contains(strings.ToLower(b.Fonts.Display.Family), "narrow") || strings.Contains(strings.ToLower(b.Fonts.Display.Family), "condensed") {
		b.Fonts.Display.Advance = 0.47
	}
	return problems
}

// deriveAccents picks the accent tokens from their roles: "accent" is the
// signature, "urgency" the time-bound one. Each needs an approved text
// pairing on its own fill.
func deriveAccents(g *guideTokens, roles map[string]string, b *posterrender.Brand) []string {
	var problems []string
	names := sortedKeys(b.Tokens)
	for _, name := range names {
		r := roles[name]
		if !strings.Contains(r, "accent") && !strings.Contains(r, "urgen") && !strings.Contains(r, "highlight") {
			continue
		}
		// Accent text is large (an eyebrow, a badge), so a pairing the
		// guide approves only above a size still qualifies.
		text := bestFg(b, name, nil, true)
		if text == "" {
			problems = append(problems, fmt.Sprintf("accent %s has no approved text pairing on it", name))
			continue
		}
		b.Accents[name] = posterrender.Accent{Fill: name, Text: text}
	}
	if len(b.Accents) == 0 {
		problems = append(problems, "no color has an accent role (a role mentioning 'accent' or 'urgency')")
	}
	b.AccentRules = posterrender.AccentRules{MaxElements: 1, Together: false}
	if g.Constraints.MaxAccentElements != nil {
		b.AccentRules.MaxElements = *g.Constraints.MaxAccentElements
	}
	if g.Constraints.MaxAccent != nil {
		b.AccentRules.MaxElements = *g.Constraints.MaxAccent
	}
	if g.Constraints.AmberAndEmberTogether != nil {
		b.AccentRules.Together = *g.Constraints.AmberAndEmberTogether
	}
	if g.Constraints.AccentsTogether != nil {
		b.AccentRules.Together = *g.Constraints.AccentsTogether
	}
	return problems
}

// deriveGrounds picks background tokens from roles mentioning "ground"
// and, for each, the text, label and rule tokens from the pairings.
func deriveGrounds(roles, logoOn map[string]string, b *posterrender.Brand) []string {
	var problems []string
	for _, name := range sortedKeys(b.Tokens) {
		if !strings.Contains(roles[name], "ground") {
			continue
		}
		text := bestFg(b, name, b.Accents, false)
		if text == "" {
			problems = append(problems, fmt.Sprintf("ground %s has no approved text pairing on it", name))
			continue
		}
		label := secondFg(b, name, text)
		b.Grounds[name] = posterrender.Ground{Bg: name, Text: text, Label: label, Rule: ruleToken(b, name, text), Logo: logoFor(name, logoOn, b)}
	}
	if len(b.Grounds) == 0 {
		problems = append(problems, "no color has a ground role (a role mentioning 'ground')")
	}
	return problems
}

// bestFg is the approved foreground with the best contrast on bg, skipping
// accents (an accent used as text would spend the one-per-piece budget) and,
// unless allowMin, pairings approved only above a type size (body copy
// cannot rely on those).
func bestFg(b *posterrender.Brand, bg string, skip map[string]posterrender.Accent, allowMin bool) string {
	best, bestRatio := "", 0.0
	for _, p := range b.Pairs {
		if p.Bg != bg || (p.MinSizePx > 0 && !allowMin) {
			continue
		}
		if _, isAccent := skip[p.Fg]; isAccent {
			continue
		}
		if r := contrast(b.Tokens[p.Fg], b.Tokens[bg]); r > bestRatio {
			best, bestRatio = p.Fg, r
		}
	}
	return best
}

// secondFg is a second approved text color on bg for labels: the next
// best contrast at or above 4.5, else the text color itself.
func secondFg(b *posterrender.Brand, bg, text string) string {
	best, bestRatio := text, 0.0
	for _, p := range b.Pairs {
		if p.Bg != bg || p.Fg == text || p.MinSizePx > 0 {
			continue
		}
		if _, isAccent := b.Accents[p.Fg]; isAccent {
			continue
		}
		if r := contrast(b.Tokens[p.Fg], b.Tokens[bg]); r >= 4.5 && r > bestRatio {
			best, bestRatio = p.Fg, r
		}
	}
	return best
}

// ruleToken is the quietest token against bg that is still visible: a
// divider should be seen, not read.
func ruleToken(b *posterrender.Brand, bg, text string) string {
	best, bestRatio := text, math.MaxFloat64
	for _, name := range sortedKeys(b.Tokens) {
		if name == bg || name == text {
			continue
		}
		if _, isAccent := b.Accents[name]; isAccent {
			continue
		}
		r := contrast(b.Tokens[name], b.Tokens[bg])
		if r > 1.15 && r < bestRatio {
			best, bestRatio = name, r
		}
	}
	return best
}

// logoFor is the variant the guide names for this ground, else the
// conventional one by luminance.
func logoFor(ground string, logoOn map[string]string, b *posterrender.Brand) string {
	if v, ok := logoOn[ground]; ok {
		return v
	}
	if luminance(b.Tokens[ground]) < 0.3 {
		return "white"
	}
	return "color"
}

// readLogoMap reads the guide's logo.on_<ground> keys.
func readLogoMap(g *guideTokens) map[string]string {
	logoOn := map[string]string{}
	for k, v := range g.LogoRaw {
		if !strings.HasPrefix(k, "on_") {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			logoOn[strings.TrimPrefix(k, "on_")] = s
		}
	}
	return logoOn
}

func deriveFormats(g *guideTokens, b *posterrender.Brand) []string {
	for name, c := range g.Canvas {
		if c.W <= 0 || c.H <= 0 {
			continue
		}
		side := c.Safe
		if side == 0 {
			side = 60
		}
		f := posterrender.Format{Width: c.W, Height: c.H, Safe: posterrender.SafeMargin{Top: side, Right: side, Bottom: side, Left: side}}
		if c.SafeTop > 0 {
			f.Safe.Top = c.SafeTop
		}
		if c.SafeBottom > 0 {
			f.Safe.Bottom = c.SafeBottom
		}
		if c.SafeLeft > 0 {
			f.Safe.Left = c.SafeLeft
		}
		if c.SafeRight > 0 {
			f.Safe.Right = c.SafeRight
		}
		b.Formats[name] = f
	}
	if len(b.Formats) == 0 {
		return []string{"the guide defines no canvas sizes"}
	}
	b.PortraitFormat = portraitFormat(b.Formats)
	return nil
}

// portraitFormat is the canvas the event poster uses: the one named
// portrait, else the tallest at least 1000px wide, else the first.
func portraitFormat(formats map[string]posterrender.Format) string {
	names := sortedKeys(formats)
	for _, n := range names {
		if strings.Contains(n, "portrait") {
			return n
		}
	}
	best, bestRatio := "", 0.0
	for _, n := range names {
		f := formats[n]
		if f.Width < 1000 {
			continue
		}
		if r := float64(f.Height) / float64(f.Width); r > bestRatio {
			best, bestRatio = n, r
		}
	}
	if best == "" && len(names) > 0 {
		best = names[0]
	}
	return best
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// luminance is WCAG relative luminance of a #RRGGBB.
func luminance(hexv string) float64 {
	if len(hexv) != 7 {
		return 0
	}
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hexv[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
