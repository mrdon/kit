package tools

import (
	"encoding/json"
)

func registerWebTools(r *Registry) {
	r.Register(Def{
		Name:        "web_fetch",
		Description: "Fetch a web page. Returns title + heading outline by default. Use 'heading' to read a specific section, or 'query' to search the page content.",
		Schema: propsReq(map[string]any{
			"url":     field("string", "The URL to fetch"),
			"heading": field("string", "Optional: heading name to read that section"),
			"query":   field("string", "Optional: search the page content for this text"),
		}, "url"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				URL     string `json:"url"`
				Heading string `json:"heading"`
				Query   string `json:"query"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}

			page, err := ec.Fetcher.Fetch(ec.Ctx, inp.URL)
			if err != nil {
				return "Failed to fetch: " + err.Error(), nil
			}

			// If both heading and query, search within matching sections
			if inp.Heading != "" && inp.Query != "" {
				sections := page.FindSections(inp.Heading)
				if sections == "" {
					return page.SearchContent(inp.Query), nil
				}
				return sections, nil
			}

			if inp.Heading != "" {
				return page.FindSections(inp.Heading), nil
			}

			if inp.Query != "" {
				return page.SearchContent(inp.Query), nil
			}

			// Default: heading outline
			return page.Outline(), nil
		},
	})
}
