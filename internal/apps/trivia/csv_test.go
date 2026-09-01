package trivia

import (
	"strings"
	"testing"
)

func parse(t *testing.T, body string) ImportPlan {
	t.Helper()
	plan, err := ParseCSV(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	return plan
}

// The ordinary sheet.
func TestParseCSVHappyPath(t *testing.T) {
	plan := parse(t, "question,topics,answer\n"+
		"How many bones in the human body?,Science;Body,206\n"+
		"What year did Apollo 11 land?,Space;History,1969\n")

	if len(plan.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 (errors: %v)", len(plan.Rows), plan.Errors)
	}
	if plan.Rows[0].AnswerValue != 206 {
		t.Fatalf("answer = %v, want 206", plan.Rows[0].AnswerValue)
	}
	if len(plan.Rows[0].Topics) != 2 {
		t.Fatalf("topics = %v, want 2", plan.Rows[0].Topics)
	}
	if plan.Rows[0].Topics[0].Key != "science" || plan.Rows[0].Topics[0].Label != "Science" {
		t.Fatalf("topic = %+v, want key science with the typed spelling", plan.Rows[0].Topics[0])
	}
}

// Header names are matched case-insensitively, by alias, and in any order --
// sheets come from everywhere and rearranging one by hand before upload is
// not a thing a host should have to do.
func TestParseCSVHeaderAliasesAndOrder(t *testing.T) {
	plan := parse(t, "ANSWER,Category,Prompt\n42,Trivia,The ultimate question\n")
	if len(plan.Rows) != 1 {
		t.Fatalf("got %d rows, want 1 (errors: %v)", len(plan.Rows), plan.Errors)
	}
	if plan.Rows[0].Prompt != "The ultimate question" || plan.Rows[0].AnswerValue != 42 {
		t.Fatalf("row = %+v", plan.Rows[0])
	}
}

// Excel writes a BOM. Without stripping it the first header cell never
// matches and the host gets "missing required column: question" for a file
// whose first column plainly says question.
func TestParseCSVStripsBOM(t *testing.T) {
	plan := parse(t, "\ufeffquestion,topics,answer\nHow tall?,Places,300\n")
	if len(plan.Rows) != 1 {
		t.Fatalf("BOM defeated the header match: %v", plan.Errors)
	}
}

// The answer normaliser, which is also what the phone reuses.
func TestParseCSVAnswerNormalisation(t *testing.T) {
	cases := map[string]float64{
		`"$1,200"`: 1200, "1.5%": 1.5, "-40": -40, "1e3": 1000,
		"0": 0, "  206  ": 206, "3_000": 3000,
	}
	for in, want := range cases {
		plan := parse(t, "question,topics,answer\nQ,T,"+in+"\n")
		if len(plan.Rows) != 1 {
			t.Fatalf("%q was rejected: %v", in, plan.Errors)
		}
		if plan.Rows[0].AnswerValue != want {
			t.Errorf("%q parsed to %v, want %v", in, plan.Rows[0].AnswerValue, want)
		}
	}
}

// An unquoted comma inside a value makes a row wider than its header. Taking
// the columns positionally would read "$1" as the answer and import 1 — a
// wrong answer that looks right until it is read out to a room. It is refused
// instead, with a message naming the cause.
func TestParseCSVRefusesRowsWidenedByAnUnquotedComma(t *testing.T) {
	plan := parse(t, "question,topics,answer\nHow much?,Money,$1,200\n")
	if len(plan.Rows) != 0 {
		t.Fatalf("imported %v — an unquoted comma must not be read positionally", plan.Rows[0].AnswerValue)
	}
	if len(plan.Errors) != 1 || !strings.Contains(plan.Errors[0].Message, "unquoted comma") {
		t.Fatalf("errors = %v, want one naming the unquoted comma", plan.Errors)
	}
}

// A partial parse is what turns "12 feet" into 12 and produces an argument at
// the bar, so ParseFloat has to consume the whole string.
func TestParseCSVRejectsPartialNumbers(t *testing.T) {
	for _, bad := range []string{"12 feet", "about 40", "twelve", "1..2", ""} {
		plan := parse(t, "question,topics,answer\nQ,T,"+bad+"\n")
		if len(plan.Rows) != 0 {
			t.Errorf("%q was accepted as %v", bad, plan.Rows[0].AnswerValue)
		}
	}
}

// A bad row reports its own line number and keeps the good rows.
func TestParseCSVReportsTheRightLineAndImportsTheRest(t *testing.T) {
	plan := parse(t, "question,topics,answer\n"+
		"Good one,Science,10\n"+
		"Bad one,Science,not a number\n"+
		"Another good,Science,20\n")

	if len(plan.Rows) != 2 {
		t.Fatalf("got %d good rows, want 2 — a typo must not fail the sheet", len(plan.Rows))
	}
	if len(plan.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1", plan.Errors)
	}
	if plan.Errors[0].Line != 3 {
		t.Fatalf("error reported on line %d, want 3", plan.Errors[0].Line)
	}
	if !strings.Contains(plan.Errors[0].Message, "not a number") {
		t.Fatalf("message = %q", plan.Errors[0].Message)
	}
}

// A missing column names what it found alongside what it wanted, because the
// host is looking at a spreadsheet and needs to know which header to fix.
func TestParseCSVMissingColumnNamesFoundVsExpected(t *testing.T) {
	_, err := ParseCSV(strings.NewReader("question,answer\nQ,10\n"))
	if err == nil {
		t.Fatal("expected an error for a sheet with no topics column")
	}
	msg := err.Error()
	if !strings.Contains(msg, "topics") || !strings.Contains(msg, "question") {
		t.Fatalf("message = %q, want it to name both the missing and the found columns", msg)
	}
}

// There is NO positional fallback, and this is the test that says so. A
// headerless file importing question text as answers is the worst failure
// available here — it produces a bank that looks fine and scores nonsense in
// front of a room.
func TestParseCSVNeverFallsBackToPositionalColumns(t *testing.T) {
	_, err := ParseCSV(strings.NewReader("How many bones?,Science,206\nWhat year?,Space,1969\n"))
	if err == nil {
		t.Fatal("a headerless file was accepted — it must be rejected, not guessed at")
	}
}

// CRLF from a Windows export.
func TestParseCSVHandlesCRLF(t *testing.T) {
	plan := parse(t, "question,topics,answer\r\nHow tall?,Places,300\r\n")
	if len(plan.Rows) != 1 {
		t.Fatalf("CRLF broke the parse: %v", plan.Errors)
	}
	if plan.Rows[0].Prompt != "How tall?" {
		t.Fatalf("prompt = %q — a stray CR survived", plan.Rows[0].Prompt)
	}
}

// One ragged line costs that line, not the file. csv.Reader's default would
// abort the whole import on the first arity mismatch.
func TestParseCSVRaggedRowDoesNotAbortTheFile(t *testing.T) {
	plan := parse(t, "question,topics,answer\n"+
		"Fine,Science,10\n"+
		"Short row\n"+
		"Also fine,Science,20\n")

	if len(plan.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 — one ragged line must not abort the sheet", len(plan.Rows))
	}
	if len(plan.Errors) != 1 || plan.Errors[0].Line != 3 {
		t.Fatalf("errors = %v, want one on line 3", plan.Errors)
	}
}

// Duplicates inside the file collapse rather than importing twice.
func TestParseCSVDedupesWithinTheFile(t *testing.T) {
	plan := parse(t, "question,topics,answer\n"+
		"How many bones?,Science,206\n"+
		"  how many BONES?  ,Science,206\n")
	if len(plan.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(plan.Rows))
	}
	if plan.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", plan.Skipped)
	}
}

// Fifty is enough to see the pattern; a thousand is a wall.
func TestParseCSVTruncatesTheErrorList(t *testing.T) {
	var b strings.Builder
	b.WriteString("question,topics,answer\n")
	for range 51 {
		b.WriteString("Q,T,nope\n")
	}
	plan := parse(t, b.String())
	if len(plan.Errors) != maxImportErrors {
		t.Fatalf("got %d errors, want capped at %d", len(plan.Errors), maxImportErrors)
	}
	if !plan.Truncated {
		t.Fatal("Truncated not set — the host would think 50 was the whole story")
	}
}

// Every question needs at least one topic, or it can never reach a board.
func TestParseCSVRequiresATopic(t *testing.T) {
	plan := parse(t, "question,topics,answer\nQ,   ;  ,10\n")
	if len(plan.Rows) != 0 {
		t.Fatal("a row with no usable topic was accepted")
	}
	if len(plan.Errors) != 1 || !strings.Contains(plan.Errors[0].Message, "topics") {
		t.Fatalf("errors = %v", plan.Errors)
	}
}

// Five is the cap, and topics that differ only in case are one topic.
func TestParseCSVTopicCapAndCaseFolding(t *testing.T) {
	plan := parse(t, "question,topics,answer\nQ,a;b;c;d;e;f;g,10\n")
	if got := len(plan.Rows[0].Topics); got != maxTopicsPerQuestion {
		t.Fatalf("kept %d topics, want %d", got, maxTopicsPerQuestion)
	}
	plan = parse(t, "question,topics,answer\nQ,Sports;sports;SPORTS,10\n")
	if got := len(plan.Rows[0].Topics); got != 1 {
		t.Fatalf("kept %d topics, want 1 — case must not make three columns", got)
	}
	if plan.Rows[0].Topics[0].Label != "Sports" {
		t.Fatalf("label = %q, want the first spelling", plan.Rows[0].Topics[0].Label)
	}
}

// Blank lines between sections of a hand-edited sheet are not errors.
func TestParseCSVSkipsBlankLines(t *testing.T) {
	plan := parse(t, "question,topics,answer\nQ1,T,1\n\n,,\nQ2,T,2\n")
	if len(plan.Rows) != 2 || len(plan.Errors) != 0 {
		t.Fatalf("rows=%d errors=%v", len(plan.Rows), plan.Errors)
	}
}

// A prompt too long to read at 96px across a bar is rejected at import, not
// discovered on the TV.
func TestParseCSVRejectsOverlongPrompts(t *testing.T) {
	long := strings.Repeat("x", maxPromptLength+1)
	plan := parse(t, "question,topics,answer\n"+long+",T,10\n")
	if len(plan.Rows) != 0 {
		t.Fatal("an overlong prompt was accepted")
	}
}

// An empty file says so rather than failing obscurely.
func TestParseCSVEmptyFile(t *testing.T) {
	if _, err := ParseCSV(strings.NewReader("")); err == nil {
		t.Fatal("expected an error for an empty file")
	}
}
