package posters

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/mrdon/kit/internal/services"
)

// copy_poster_layout: "make it like that one". The other poster's source
// is taken whole (its template, ground, accent and any hand edits) and the
// inlined content and photos are swapped for this event's, so nothing of
// the other event's words or pictures carries over. Deterministic: no
// model rewrites code to copy a look.

// contentBlock is the pretty-printed content constant inlinePoster writes.
var contentBlock = regexp.MustCompile(`(?s)\nconst content = (\{.*?\n\});\n`)

type copyLayoutArg struct {
	PosterID     string `json:"poster_id"`
	FromPosterID string `json:"from_poster_id"`
}

func (a *App) coreCopyLayout(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in copyLayoutArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	fromID, err := parseID(in.FromPosterID, "from_poster_id")
	if err != nil {
		return nil, err
	}
	if id == fromID {
		return nil, invalid("from_poster_id is this poster")
	}
	poster, cur, err := a.loadPoster(ctx, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, invalid("this poster has no version yet; generate options first so there is content to carry over")
	}
	from, fromCur, err := a.loadPoster(ctx, caller.TenantID, fromID)
	if err != nil {
		return nil, err
	}
	if fromCur == nil {
		return nil, invalid("that poster has no version to copy")
	}
	source, ok := swapInlined(fromCur.Source, cur)
	if !ok {
		return nil, invalid("that poster was restructured in chat, so its layout cannot be copied mechanically; read it with get_poster and adapt its source to this event by hand")
	}
	label := strOr(from.Title, fromID.String())
	res, err := a.saveEdit(ctx, caller.TenantID, poster, source, "Layout from "+label, AuthorAgent)
	if err != nil {
		return nil, err
	}
	return editOutcome(res, fmt.Sprintf("Saved as a new version with the layout of %q", label))
}

// swapInlined puts this version's content and photos into another
// poster's source, keeping that source's template, ground and accent. The
// photos are this poster's own; a layout that needs one when this poster
// has none fails validation, which is the right message.
func swapInlined(otherSource string, mine *Version) (string, bool) {
	if !contentBlock.MatchString(otherSource) {
		return "", false
	}
	_, _, ground, ok := readInlined(otherSource)
	if !ok {
		return "", false
	}
	content, err := json.MarshalIndent(mine.Content, "", "  ")
	if err != nil {
		return "", false
	}
	source := contentBlock.ReplaceAllLiteralString(otherSource, "\nconst content = "+string(content)+";\n")
	return writeInlined(source, mine.Photos, ground), true
}
