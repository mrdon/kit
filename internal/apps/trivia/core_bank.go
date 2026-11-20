package trivia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

type listQuestionsInput struct {
	Pack      string `json:"pack"`
	Questions bool   `json:"questions"`
	Search    string `json:"search"`
}

// coreListQuestions is trivia_list_questions: the bank by pack, summaries
// always, questions on request. The summaries alone are small enough to ask
// for casually; the full bank is hundreds of lines, which is why it is opt-in,
// and why search exists: checking a new pack for overlap wants the twenty
// Christmas questions, not all five hundred.
func coreListQuestions(ctx context.Context, caller *services.Caller, pool *pgxpool.Pool, raw json.RawMessage) (string, error) {
	var in listQuestionsInput
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	sets, err := ListDatasets(ctx, pool, caller.TenantID)
	if err != nil {
		return "", err
	}
	if in.Pack != "" {
		d := findDataset(sets, in.Pack)
		if d == nil {
			return fmt.Sprintf("No pack named %q. Packs: %s.", in.Pack, packNames(sets)), nil
		}
		sets = []Dataset{*d}
	}
	if len(sets) == 0 {
		return "The question bank is empty.", nil
	}
	ids := make([]uuid.UUID, 0, len(sets))
	for _, d := range sets {
		ids = append(ids, d.ID)
	}
	byDataset, err := BankQuestions(ctx, pool, caller.TenantID, ids)
	if err != nil {
		return "", err
	}

	terms := searchTerms(in.Search)
	var b strings.Builder
	for _, d := range sets {
		qs := byDataset[d.ID]
		hits := qs
		if len(terms) > 0 {
			hits = matchingQuestions(qs, terms)
			if len(hits) == 0 {
				continue
			}
		}
		fmt.Fprintf(&b, "*%s*: %d question%s, %d never asked, average difficulty %s",
			d.Name, d.Questions, plural(d.Questions), d.Fresh, averageDifficulty(qs))
		if d.Notes != "" {
			fmt.Fprintf(&b, ". %s", d.Notes)
		}
		b.WriteString("\n")
		if !in.Questions && len(terms) == 0 {
			continue
		}
		for _, q := range hits {
			fmt.Fprintf(&b, "- [%s] %s = %s (%s)\n", difficultyLabel(q.Difficulty), q.Prompt, q.AnswerText, joinTopicLabels(q.Topics))
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return fmt.Sprintf("No questions match %q.", in.Search), nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// searchTerms splits a comma-separated search into lowercase terms, so one
// call can ask for "christmas, hanukkah, santa".
func searchTerms(search string) []string {
	var out []string
	for part := range strings.SplitSeq(search, ",") {
		if t := strings.ToLower(strings.TrimSpace(part)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// matchingQuestions keeps the questions whose prompt or any topic contains
// one of the terms, case-insensitively.
func matchingQuestions(qs []Question, terms []string) []Question {
	var out []Question
	for _, q := range qs {
		hay := strings.ToLower(q.Prompt + " " + joinTopicLabels(q.Topics))
		for _, t := range terms {
			if strings.Contains(hay, t) {
				out = append(out, q)
				break
			}
		}
	}
	return out
}

type importPackInput struct {
	Name    string `json:"name"`
	Notes   string `json:"notes"`
	CSV     string `json:"csv"`
	Replace bool   `json:"replace"`
}

// coreImportPack is trivia_import_pack. It writes through importQuestions,
// exactly as the console upload does, with two refusals the console does not
// need because a person there sees what they are replacing:
//
//   - an existing pack is only replaced when the caller says so, and
//   - never one a game has asked from (or a shipped starter pack). Replacing
//     a played pack would quietly reset which of its questions are fresh, and
//     a host who meant to do that can still do it in the console.
func coreImportPack(ctx context.Context, caller *services.Caller, pool *pgxpool.Pool, raw json.RawMessage) (string, error) {
	var in importPackInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "Could not read the input: " + err.Error(), nil
	}
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return "A pack needs a name.", nil
	case len(name) > 60:
		return "A pack name is at most 60 characters.", nil
	case len(in.CSV) > importMaxBytes:
		return "That CSV is too large.", nil
	}

	sets, err := ListDatasets(ctx, pool, caller.TenantID)
	if err != nil {
		return "", err
	}
	if d := findDataset(sets, name); d != nil {
		if msg := replaceRefusal(d, in.Replace); msg != "" {
			return msg, nil
		}
	}

	plan, err := ParseCSV(strings.NewReader(in.CSV))
	if err != nil {
		return "Could not read the CSV: " + err.Error(), nil
	}
	if len(plan.Rows) == 0 {
		return "No usable rows, so nothing was imported.\n" + rowErrorLines(plan.Errors, plan.Truncated), nil
	}

	resp, err := importQuestions(ctx, pool, caller.TenantID, plan, importTarget{
		Name:    name,
		Notes:   strings.TrimSpace(in.Notes),
		Replace: true,
	})
	if errors.Is(err, ErrDatasetInUse) {
		return "That pack is on the board of a game still in play. Finish or delete that game first.", nil
	}
	if err != nil {
		return "", err
	}
	return importSummary(name, plan, resp), nil
}

func replaceRefusal(d *Dataset, replace bool) string {
	switch {
	case d.BuiltinKey != "":
		return fmt.Sprintf("%q is a starter pack Kit ships. Pick another name.", d.Name)
	case !replace:
		return fmt.Sprintf("A pack named %q already exists (%d questions). Pass replace=true to replace it, or pick another name.",
			d.Name, d.Questions)
	case d.Fresh < d.Questions:
		return fmt.Sprintf("%q has been played (%d of %d questions asked), so it can't be replaced here. Replace it in the console, or pick another name.",
			d.Name, d.Questions-d.Fresh, d.Questions)
	}
	return ""
}

func importSummary(name string, plan ImportPlan, resp importResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported %d question%s into *%s*", len(plan.Rows), plural(len(plan.Rows)), name)
	unrated := 0
	for _, q := range plan.Rows {
		if q.Difficulty == nil {
			unrated++
		}
	}
	fmt.Fprintf(&b, ", average difficulty %s", averageDifficulty(plan.Rows))
	if unrated > 0 {
		fmt.Fprintf(&b, " (%d unrated)", unrated)
	}
	b.WriteString(".\n")
	if resp.SkippedDuplicates > 0 {
		fmt.Fprintf(&b, "Skipped %d duplicate row%s.\n", resp.SkippedDuplicates, plural(resp.SkippedDuplicates))
	}
	if len(resp.Errors) > 0 {
		b.WriteString(rowErrorLines(resp.Errors, resp.Truncated))
	}
	labels := make([]string, 0, len(resp.Topics))
	for _, t := range resp.Topics {
		labels = append(labels, fmt.Sprintf("%s (%d)", t.Label, t.Total))
	}
	fmt.Fprintf(&b, "Topics: %s.", strings.Join(labels, ", "))
	return b.String()
}

func rowErrorLines(errs []RowError, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d row%s skipped:\n", len(errs), plural(len(errs)))
	for _, e := range errs {
		fmt.Fprintf(&b, "- line %d: %s\n", e.Line, e.Message)
	}
	if truncated {
		b.WriteString("- (more errors not shown)\n")
	}
	return b.String()
}

func findDataset(sets []Dataset, name string) *Dataset {
	key := FoldKey(name)
	for i := range sets {
		if FoldKey(sets[i].Name) == key {
			return &sets[i]
		}
	}
	return nil
}

func packNames(sets []Dataset) string {
	names := make([]string, 0, len(sets))
	for _, d := range sets {
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return "none yet"
	}
	return strings.Join(names, ", ")
}

func averageDifficulty(qs []Question) string {
	sum, n := 0, 0
	for _, q := range qs {
		if q.Difficulty != nil {
			sum += *q.Difficulty
			n++
		}
	}
	if n == 0 {
		return "unrated"
	}
	return fmt.Sprintf("%.1f", float64(sum)/float64(n))
}

func difficultyLabel(d *int) string {
	if d == nil {
		return "-"
	}
	return strconv.Itoa(*d)
}

func joinTopicLabels(ts []Topic) string {
	labels := make([]string, 0, len(ts))
	for _, t := range ts {
		labels = append(labels, t.Label)
	}
	return strings.Join(labels, "; ")
}
