package menu

import "strings"

// Finding a beer's description by name, tolerantly.
//
// Descriptions are filed under a normalised name and read back by the name
// on the board, and the two are typed by different people at different
// times: "Mr Radar Nitro" pushed in by an agent, "Mr. Radar (Nitro)" on the
// Untappd board. normalizeBeerName drops the parenthetical, so the two
// landed on different keys and the board row printed blank while the
// description sat in the store under a name one word longer. A sync then
// reported it missing, which read as the sync having lost it.
//
// findNote tries the name with its parenthetical kept, then without, then
// the same containment match the Untappd page lookup uses, so a description
// filed under any reasonable spelling of the beer is found.

// normalizeBeerNameFull is normalizeBeerName with the parenthetical kept as
// words: "Mr. Radar (Nitro)" -> "mr radar nitro".
func normalizeBeerNameFull(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonWordRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// noteKeys lists the keys a name may be filed under, most specific first.
func noteKeys(name string) []string {
	full, short := normalizeBeerNameFull(name), normalizeBeerName(name)
	if full == "" {
		return nil
	}
	if short == "" || short == full {
		return []string{full}
	}
	return []string{full, short}
}

// findNote looks name up in a map keyed by normalised names. The value is
// trimmed; an empty stored value counts as absent.
func findNote(m map[string]string, name string) (string, bool) {
	if len(m) == 0 {
		return "", false
	}
	keys := noteKeys(name)
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v, true
		}
	}
	// Containment, either way round, between names long enough that an
	// overlap is not a coincidence. The longest candidate wins, so "mr radar
	// nitro" beats "mr radar" for a nitro row when both are stored.
	best, bestLen := "", 0
	for _, key := range keys {
		if len(key) < 4 {
			continue
		}
		for cand, v := range m {
			if len(cand) < 4 || strings.TrimSpace(v) == "" {
				continue
			}
			if !strings.Contains(cand, key) && !strings.Contains(key, cand) {
				continue
			}
			if len(cand) > bestLen {
				best, bestLen = strings.TrimSpace(v), len(cand)
			}
		}
	}
	return best, best != ""
}

// foldNotes re-keys a hand-typed map (config.notes, keyed by whatever the
// person wrote) by normalised full name, so findNote can read it. Later
// entries never silently replace earlier ones that fold to the same key:
// the longer original key wins, on the grounds that it says more.
func foldNotes(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	won := map[string]string{}
	for name, note := range m {
		if strings.TrimSpace(note) == "" {
			continue
		}
		key := normalizeBeerNameFull(name)
		if key == "" {
			continue
		}
		if prev, ok := won[key]; ok && len(prev) >= len(name) {
			continue
		}
		won[key] = name
		out[key] = note
	}
	return out
}

// mergedNotes is the one resolution order for descriptions: what somebody
// wrote (config.notes) wins over what was scraped or pushed in (the cache).
// Returns a new map; neither input is touched.
func mergedNotes(cache, written map[string]string) map[string]string {
	out := make(map[string]string, len(cache)+len(written))
	for k, v := range cache {
		out[k] = v
	}
	for k, v := range foldNotes(written) {
		out[k] = v
	}
	return out
}
