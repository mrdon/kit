package trivia

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// maxImportErrors caps what a bad file reports. Fifty lines is enough to see
// the pattern; a thousand is a wall the host scrolls past.
const maxImportErrors = 50

// maxTopicsPerQuestion bounds the topic list. A question in nine categories
// is a data-entry accident, not a rich question.
const maxTopicsPerQuestion = 5

// maxPromptLength keeps a prompt readable at 96px on a TV across a bar.
const maxPromptLength = 500

// ImportPlan is what ParseCSV produces: the rows worth writing, plus enough
// detail for the host to fix the sheet.
//
// A three-hundred-row sheet with two typos is not a total failure, so the
// good rows import and the bad ones are reported by line number.
type ImportPlan struct {
	Rows      []Question
	Skipped   int
	Errors    []RowError
	Truncated bool
}

// RowError names a line and what was wrong with it.
type RowError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ParseCSV reads a question sheet. Pure: no database, no request, so the
// whole parsing contract is testable directly.
//
// A HEADER IS REQUIRED and there is deliberately no positional fallback. A
// headerless file silently importing question text into the answer column is
// the worst failure available here -- it produces a bank that looks fine and
// scores nonsense in front of a room.
func ParseCSV(r io.Reader) (ImportPlan, error) {
	var plan ImportPlan

	cr := csv.NewReader(stripBOM(r))
	cr.TrimLeadingSpace = true
	// Validate arity per row instead of letting the reader abort the file, so
	// one ragged line costs one line rather than the whole sheet.
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return plan, errors.New("the file is empty")
		}
		return plan, fmt.Errorf("reading csv header: %w", err)
	}
	cols, err := mapColumns(header)
	if err != nil {
		return plan, err
	}

	seen := map[string]bool{}
	line := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			plan.addError(line, err.Error())
			continue
		}
		if isBlank(rec) {
			continue
		}
		q, perr := parseRow(rec, cols, len(header))
		if perr != "" {
			plan.addError(line, perr)
			continue
		}
		if seen[q.PromptKey] {
			plan.Skipped++
			continue
		}
		seen[q.PromptKey] = true
		plan.Rows = append(plan.Rows, q)
	}
	return plan, nil
}

func (p *ImportPlan) addError(line int, msg string) {
	if len(p.Errors) >= maxImportErrors {
		p.Truncated = true
		return
	}
	p.Errors = append(p.Errors, RowError{Line: line, Message: msg})
}

// columnMap is where each field was found. Matching is case-insensitive and
// position-independent, so a sheet exported from anywhere works without being
// rearranged first.
type columnMap struct{ prompt, topics, answer int }

var columnAliases = map[string][]string{
	"question": {"question", "prompt", "q"},
	"topics":   {"topics", "topic", "category", "categories"},
	"answer":   {"answer", "value", "a"},
}

func mapColumns(header []string) (columnMap, error) {
	cm := columnMap{-1, -1, -1}
	for i, h := range header {
		key := FoldKey(h)
		switch {
		case matchesAlias(key, columnAliases["question"]):
			if cm.prompt < 0 {
				cm.prompt = i
			}
		case matchesAlias(key, columnAliases["topics"]):
			if cm.topics < 0 {
				cm.topics = i
			}
		case matchesAlias(key, columnAliases["answer"]):
			if cm.answer < 0 {
				cm.answer = i
			}
		}
	}
	var missing []string
	if cm.prompt < 0 {
		missing = append(missing, "question")
	}
	if cm.topics < 0 {
		missing = append(missing, "topics")
	}
	if cm.answer < 0 {
		missing = append(missing, "answer")
	}
	if len(missing) > 0 {
		return cm, fmt.Errorf("missing required column(s) %s — found %s",
			strings.Join(missing, ", "), strings.Join(header, ", "))
	}
	return cm, nil
}

func matchesAlias(key string, aliases []string) bool {
	return slices.Contains(aliases, key)
}

func parseRow(rec []string, cols columnMap, width int) (Question, string) {
	// Arity has to match the header EXACTLY, and the extra-fields case is
	// the one that matters: a host who typed $1,200 without quoting it
	// produces a four-field row, and reading the columns positionally would
	// take "$1" as the answer and import 1. That is a wrong answer that
	// looks right until it is read out to a room, so it is refused here with
	// a message naming the likely cause.
	if len(rec) != width {
		hint := ""
		if len(rec) > width {
			hint = " — an unquoted comma inside a value (like $1,200) does this"
		}
		return Question{}, fmt.Sprintf("row has %d fields, expected %d%s", len(rec), width, hint)
	}

	prompt := strings.TrimSpace(rec[cols.prompt])
	if prompt == "" {
		return Question{}, "question is empty"
	}
	if len(prompt) > maxPromptLength {
		return Question{}, fmt.Sprintf("question is %d characters, limit is %d", len(prompt), maxPromptLength)
	}

	topics := parseTopics(rec[cols.topics])
	if len(topics) == 0 {
		return Question{}, "no topics — every question needs at least one"
	}

	raw := strings.TrimSpace(rec[cols.answer])
	value, ok := ParseAnswer(raw)
	if !ok {
		return Question{}, fmt.Sprintf("answer %q is not a number", raw)
	}

	return Question{
		Prompt:      prompt,
		PromptKey:   FoldKey(prompt),
		AnswerValue: value,
		AnswerText:  raw,
		Topics:      topics,
	}, ""
}

// parseTopics splits on ';', drops empties, folds to keys, and keeps the
// first display spelling seen so "Sports" and "sports" become one column
// while the host still sees what they typed.
func parseTopics(field string) []Topic {
	var out []Topic
	seen := map[string]bool{}
	for part := range strings.SplitSeq(field, ";") {
		label := strings.TrimSpace(part)
		if label == "" {
			continue
		}
		key := FoldKey(label)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Topic{Key: key, Label: label})
		if len(out) == maxTopicsPerQuestion {
			break
		}
	}
	return out
}

// ParseAnswer normalises a written answer to a number: strips currency,
// grouping, percent signs and underscores, then requires ParseFloat to
// consume the WHOLE string. A partial parse is what turns "12 feet" into 12
// and produces an argument at the bar.
//
// Shared with the phone, which reuses the same rules in JS and echoes the
// parsed value back before submit.
func ParseAnswer(s string) (float64, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '$', ',', '%', '_', ' ', '\t', '\u00a0':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if cleaned == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func isBlank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// stripBOM drops a UTF-8 byte order mark, which Excel writes and which would
// otherwise make the first header cell "<BOM>question" and fail the column
// match with a message the host cannot see the cause of.
func stripBOM(r io.Reader) io.Reader {
	br := newPeeker(r)
	if br.peekBOM() {
		return br.rest(3)
	}
	return br.rest(0)
}
