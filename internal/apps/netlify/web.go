package netlify

// teamSitesGroup is one team's slice of sites, used by the console status
// JSON to group sites per team for the picker.
type teamSitesGroup struct {
	TeamName string
	TeamSlug string
	Sites    []NetlifySite
}

// groupSitesByTeam splits a flat site list into per-team groups,
// preserving discovery order and labeling unknown-team sites
// (account_name empty) as "Personal".
func groupSitesByTeam(sites []NetlifySite) []teamSitesGroup {
	indexBySlug := map[string]int{}
	out := []teamSitesGroup{}
	for _, s := range sites {
		name := s.AccountName
		if name == "" {
			name = "Personal"
		}
		slug := s.AccountSlug
		key := slug
		if key == "" {
			key = "_personal_"
		}
		idx, ok := indexBySlug[key]
		if !ok {
			out = append(out, teamSitesGroup{TeamName: name, TeamSlug: slug})
			idx = len(out) - 1
			indexBySlug[key] = idx
		}
		out[idx].Sites = append(out[idx].Sites, s)
	}
	return out
}
