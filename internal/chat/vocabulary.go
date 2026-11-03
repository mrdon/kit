package chat

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// hintLead opens the whisper prompt with jargon people ask Kit about
// that whisper's small English models mangle ("our e i n" comes back as
// "REIN"). Whisper treats the prompt as preceding speech, so a sentence
// steers it better than a bare word list does.
const hintLead = "Questions for Kit at work: our EIN, 86 a beer, the Square POS, an SOP, a W-9"

// hintMaxRunes keeps the hint inside whisper's prompt window (about 224
// tokens); whisper keeps only the tail of a longer prompt, so trimming
// here keeps the lead rather than losing it.
const hintMaxRunes = 700

// VocabularyHint builds the whisper prompt for a caller: common jargon
// plus the names of the skills they can see, which carry the business's
// own words (crowler fill, drawer close-out). Failure to read skills
// degrades to the jargon alone.
func VocabularyHint(ctx context.Context, pool *pgxpool.Pool, caller *services.Caller) string {
	var names []string
	if pool != nil && caller != nil {
		skills, err := models.GetSkillCatalog(ctx, pool, caller.TenantID, caller.UserID, caller.RoleIDs)
		if err != nil {
			slog.Warn("loading skills for voice hint", "error", err)
		}
		for _, s := range skills {
			names = append(names, s.Name)
		}
	}
	return buildVocabularyHint(names)
}

func buildVocabularyHint(skillNames []string) string {
	hint := hintLead
	for _, n := range skillNames {
		next := hint + ", " + strings.ReplaceAll(n, "-", " ")
		if len([]rune(next)) > hintMaxRunes {
			break
		}
		hint = next
	}
	return hint + "."
}
