package runtime

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestAcceptanceMugClub exercises the mug_club acceptance-test script from the
// v0.1 plan. Because this spike does not yet implement the `db.members.x`
// collection object, we call flat host functions (db_members_insert_one, etc.)
// and the script is adapted accordingly. That isolates the question "does
// Monty-the-Python-dialect execute the idioms admins will actually type"
// from "is the db accessor implemented".
func TestAcceptanceMugClub(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// In-memory store mimicking the future db.members collection.
	var (
		mu      sync.Mutex
		members []map[string]any
		nextID  = 0
	)

	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch call.Name {
		case "db_members_insert_one":
			doc, ok := call.Args["doc"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("doc arg missing/not a map: %T", call.Args["doc"])
			}
			nextID++
			id := fmt.Sprintf("m_%d", nextID)
			stored := map[string]any{}
			maps.Copy(stored, doc)
			stored["_id"] = id
			// Simulate auto _created_at as an increasing number (higher = newer)
			// so the sort assertion is deterministic.
			stored["_created_at"] = float64(nextID)
			members = append(members, stored)
			// Return a shallow copy to prevent guest-side mutation leaking in.
			out := map[string]any{}
			maps.Copy(out, stored)
			return out, nil

		case "db_members_find":
			filter, _ := call.Args["filter"].(map[string]any)
			limit := -1
			if lv, ok := call.Args["limit"].(float64); ok {
				limit = int(lv)
			}
			sortSpec, _ := call.Args["sort"].([]any)

			var matched []map[string]any
			for _, m := range members {
				ok := true
				for k, v := range filter {
					if m[k] != v {
						ok = false
						break
					}
				}
				if ok {
					matched = append(matched, m)
				}
			}
			// Apply sort: list of (field, 1|-1) tuples. Monty delivers tuples as
			// []any. Only support the first sort key for this stub.
			if len(sortSpec) > 0 {
				if tup, ok := sortSpec[0].([]any); ok && len(tup) == 2 {
					field, _ := tup[0].(string)
					dirF, _ := tup[1].(float64)
					sort.SliceStable(matched, func(i, j int) bool {
						vi, _ := matched[i][field].(float64)
						vj, _ := matched[j][field].(float64)
						if dirF < 0 {
							return vi > vj
						}
						return vi < vj
					})
				}
			}
			if limit >= 0 && len(matched) > limit {
				matched = matched[:limit]
			}
			out := make([]any, len(matched))
			for i, m := range matched {
				out[i] = m
			}
			return out, nil

		case "db_members_update_one":
			filter, _ := call.Args["filter"].(map[string]any)
			update, _ := call.Args["update"].(map[string]any)
			for _, m := range members {
				match := true
				for k, v := range filter {
					if m[k] != v {
						match = false
						break
					}
				}
				if match {
					if setOp, ok := update["$set"].(map[string]any); ok {
						maps.Copy(m, setOp)
					}
					return map[string]any{"matched": float64(1)}, nil
				}
			}
			return map[string]any{"matched": float64(0)}, nil
		}
		return nil, fmt.Errorf("unknown fn %q", call.Name)
	}

	// Note deviations from plan script:
	// - db.members.insert_one → db_members_insert_one(doc)
	// - db.members.find(filter, limit=..., sort=...) → db_members_find(filter, limit, sort)
	//   (with explicit filter positional arg)
	// - db.members.update_one → db_members_update_one(filter, update)
	code := `
def add_member(name, email, tier="silver"):
    return db_members_insert_one({
        "name": name,
        "email": email,
        "tier": tier,
    })

def list_members(tier=None, limit=50):
    filter = {"tier": tier} if tier else {}
    return db_members_find(filter, limit=limit, sort=[("_created_at", -1)])

def update_tier(member_id, tier):
    return db_members_update_one({"_id": member_id}, {"$set": {"tier": tier}})

# Exercise all three entrypoints and return a summary:
a = add_member("Alice", "a@x.com")                  # defaults to silver
b = add_member("Bob",   "b@x.com", tier="gold")
c = add_member("Carol", "c@x.com", tier="gold")

all_silver = list_members(tier="silver")
all_gold   = list_members(tier="gold")
recent_all = list_members(limit=2)

update_tier(a["_id"], "gold")
after_update = list_members(tier="gold")

{
    "a_id": a["_id"],
    "b_id": b["_id"],
    "c_id": c["_id"],
    "silver_count": len(all_silver),
    "gold_count": len(all_gold),
    "recent_all_count": len(recent_all),
    "recent_all_first_id": recent_all[0]["_id"],
    "recent_all_second_id": recent_all[1]["_id"],
    "gold_count_after_update": len(after_update),
}
`

	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler,
			Func("db_members_insert_one", "doc"),
			Func("db_members_find", "filter", "limit", "sort"),
			Func("db_members_update_one", "filter", "update"),
		))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T %v", result, result)
	}
	if m["a_id"] != "m_1" || m["b_id"] != "m_2" || m["c_id"] != "m_3" {
		t.Errorf("ids = %v / %v / %v; want m_1/m_2/m_3", m["a_id"], m["b_id"], m["c_id"])
	}
	// After initial inserts: 1 silver (Alice), 2 gold (Bob, Carol).
	if m["silver_count"] != float64(1) {
		t.Errorf("silver_count = %v, want 1", m["silver_count"])
	}
	if m["gold_count"] != float64(2) {
		t.Errorf("gold_count = %v, want 2", m["gold_count"])
	}
	// Sort by _created_at desc => newest first (Carol=m_3, then Bob=m_2).
	if m["recent_all_first_id"] != "m_3" || m["recent_all_second_id"] != "m_2" {
		t.Errorf("sort desc wrong: first=%v second=%v want m_3/m_2", m["recent_all_first_id"], m["recent_all_second_id"])
	}
	if m["recent_all_count"] != float64(2) {
		t.Errorf("recent_all_count = %v, want 2 (limit)", m["recent_all_count"])
	}
	// After upgrading Alice to gold, expect 3 gold.
	if m["gold_count_after_update"] != float64(3) {
		t.Errorf("gold_count_after_update = %v, want 3", m["gold_count_after_update"])
	}
}

// TestAcceptanceReviewTriage exercises the review_triage acceptance-test script
// from the plan. Covers: for-loop over dict list, dict indexing, string slicing,
// string concat, conditional branch, nested LLM calls, create_decision.
func TestAcceptanceReviewTriage(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	seeded := []map[string]any{
		{"_id": "r1", "text": "the burger was cold and the service was slow — awful", "triaged": false},
		{"_id": "r2", "text": "best evening ever, staff were amazing", "triaged": false},
		{"_id": "r3", "text": "you should add more vegan options please", "triaged": false},
		{"_id": "r4", "text": "meh, nothing special", "triaged": false},
	}

	var (
		mu        sync.Mutex
		decisions []map[string]any
		updates   []map[string]any
	)

	classify := func(text string) string {
		// Very simple keyword router just to drive the test deterministically.
		switch {
		case contains(text, "awful") || contains(text, "cold") || contains(text, "slow"):
			return "complaint"
		case contains(text, "amazing") || contains(text, "best"):
			return "praise"
		case contains(text, "add") || contains(text, "should"):
			return "suggestion"
		default:
			return "noise"
		}
	}

	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch call.Name {
		case "db_reviews_find":
			filter, _ := call.Args["filter"].(map[string]any)
			var out []any
			for _, r := range seeded {
				ok := true
				for k, v := range filter {
					if r[k] != v {
						ok = false
						break
					}
				}
				if ok {
					out = append(out, r)
				}
			}
			return out, nil

		case "db_reviews_update_one":
			filter, _ := call.Args["filter"].(map[string]any)
			update, _ := call.Args["update"].(map[string]any)
			updates = append(updates, map[string]any{"filter": filter, "update": update})
			for _, r := range seeded {
				match := true
				for k, v := range filter {
					if r[k] != v {
						match = false
						break
					}
				}
				if match {
					if setOp, ok := update["$set"].(map[string]any); ok {
						maps.Copy(r, setOp)
					}
				}
			}
			return map[string]any{"matched": float64(1)}, nil

		case "llm_classify":
			text, _ := call.Args["text"].(string)
			return classify(text), nil

		case "llm_generate":
			prompt, _ := call.Args["prompt"].(string)
			return "DRAFT[" + prompt + "]", nil

		case "create_decision":
			decisions = append(decisions, map[string]any{
				"title": call.Args["title"],
				"body":  call.Args["body"],
			})
			return map[string]any{"id": fmt.Sprintf("dec_%d", len(decisions))}, nil
		}
		return nil, fmt.Errorf("unknown fn %q", call.Name)
	}

	// Script adapted from plan Test 2 body, with flat host-fn names.
	code := `
def triage():
    pending = db_reviews_find({"triaged": False}, limit=50)
    updates = 0
    complaints = 0
    for r in pending:
        cat = llm_classify(text=r["text"], categories=["complaint", "praise", "suggestion", "noise"])
        db_reviews_update_one(
            {"_id": r["_id"]},
            {"$set": {"category": cat, "triaged": True}},
        )
        updates = updates + 1
        if cat == "complaint":
            draft = llm_generate(prompt="Draft an empathetic 2-sentence reply: " + r["text"])
            create_decision(
                title="Review reply needed",
                body="Original: " + r["text"][:300] + "\n\nDraft: " + draft,
            )
            complaints = complaints + 1
    return {"updates": updates, "complaints": complaints}

triage()
`

	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler,
			Func("db_reviews_find", "filter", "limit"),
			Func("db_reviews_update_one", "filter", "update"),
			Func("llm_classify", "text", "categories"),
			Func("llm_generate", "prompt"),
			Func("create_decision", "title", "body"),
		))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T %v", result, result)
	}
	if m["updates"] != float64(4) {
		t.Errorf("updates = %v, want 4", m["updates"])
	}
	if m["complaints"] != float64(1) {
		t.Errorf("complaints = %v, want 1 (only r1 is a complaint)", m["complaints"])
	}
	if len(decisions) != 1 {
		t.Errorf("len(decisions) = %d, want 1", len(decisions))
	} else {
		body, _ := decisions[0]["body"].(string)
		if !contains(body, "Draft: DRAFT[") {
			t.Errorf("decision body missing draft marker: %q", body)
		}
		if !contains(body, "Original: ") {
			t.Errorf("decision body missing Original prefix: %q", body)
		}
	}
	if len(updates) != 4 {
		t.Errorf("len(updates) = %d, want 4", len(updates))
	}
	// Confirm every review has triaged:true set in the update payload.
	for i, u := range updates {
		update := u["update"].(map[string]any)
		setOp := update["$set"].(map[string]any)
		if setOp["triaged"] != true {
			t.Errorf("update[%d] triaged = %v, want true", i, setOp["triaged"])
		}
		if _, ok := setOp["category"].(string); !ok {
			t.Errorf("update[%d] category missing or non-string: %v", i, setOp["category"])
		}
	}
}

// TestPythonIdioms runs each common admin-reached-for idiom as its own subtest
// and logs failures with the Monty error so we can decide per-idiom: support,
// document as restriction, or pivot. Uses t.Errorf + continue so one idiom
// failing doesn't hide the next.
func TestPythonIdioms(t *testing.T) {
	runner := testRunner

	// Each case: name + code snippet + predicate on the Execute result.
	type idiomCase struct {
		name   string
		code   string
		verify func(t *testing.T, result any, err error)
	}

	cases := []idiomCase{
		{
			name: "dict_get_with_default_missing_key",
			code: `{"a": 1}.get("missing", "fallback")`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "fallback" {
					t.Errorf("result = %v, want fallback", result)
				}
			},
		},
		{
			name: "dict_get_with_default_present_key",
			code: `{"a": 1}.get("a", 99)`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != float64(1) {
					t.Errorf("result = %v, want 1", result)
				}
			},
		},
		{
			name: "list_slice_head",
			code: `[1,2,3,4,5][:3]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 3 {
					t.Errorf("result = %v (type %T), want 3-element list", result, result)
				}
			},
		},
		{
			name: "list_slice_neg",
			code: `[1,2,3,4,5][:-1]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 4 {
					t.Errorf("result = %v, want 4-element list", result)
				}
			},
		},
		{
			name: "list_slice_range",
			code: `[1,2,3,4,5][1:4]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 3 {
					t.Errorf("result = %v, want 3-element list", result)
				}
			},
		},
		{
			name: "string_concat_plus",
			code: `"hello " + "world"`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "hello world" {
					t.Errorf("result = %v, want hello world", result)
				}
			},
		},
		{
			name: "fstring",
			code: `
name = "kit"
f"hello {name}"
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "hello kit" {
					t.Errorf("result = %v, want hello kit", result)
				}
			},
		},
		{
			name: "fstring_with_expr",
			code: `
n = 3
f"count={n * 2}"
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "count=6" {
					t.Errorf("result = %v, want count=6", result)
				}
			},
		},
		{
			name: "list_comprehension",
			code: `[x*2 for x in range(5)]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 5 {
					t.Errorf("result = %v, want 5-element list", result)
					return
				}
				want := []float64{0, 2, 4, 6, 8}
				for i, w := range want {
					if list[i] != w {
						t.Errorf("list[%d] = %v, want %v", i, list[i], w)
					}
				}
			},
		},
		{
			name: "list_comprehension_with_filter",
			code: `[x for x in [1,2,3,4,5] if x > 2]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 3 {
					t.Errorf("result = %v, want 3-element list", result)
				}
			},
		},
		{
			name: "len_list",
			code: `len([1,2,3,4])`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != float64(4) {
					t.Errorf("result = %v, want 4", result)
				}
			},
		},
		{
			name: "len_dict",
			code: `len({"a": 1, "b": 2, "c": 3})`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != float64(3) {
					t.Errorf("result = %v, want 3", result)
				}
			},
		},
		{
			name: "len_string",
			code: `len("hello")`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != float64(5) {
					t.Errorf("result = %v, want 5", result)
				}
			},
		},
		{
			name: "list_append",
			code: `
xs = [1, 2]
xs.append(3)
xs.append(4)
xs
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 4 {
					t.Errorf("result = %v, want 4-element list", result)
				}
			},
		},
		{
			name: "nested_dict_literal",
			code: `{"outer": {"inner": {"deep": 42}}}`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				outer, ok := result.(map[string]any)
				if !ok {
					t.Errorf("result type %T, want map", result)
					return
				}
				inner, ok := outer["outer"].(map[string]any)
				if !ok {
					t.Errorf("outer[\"outer\"] not a map: %v", outer["outer"])
					return
				}
				deep, ok := inner["inner"].(map[string]any)
				if !ok {
					t.Errorf("inner not a map: %v", inner["inner"])
					return
				}
				if deep["deep"] != float64(42) {
					t.Errorf("deep = %v, want 42", deep["deep"])
				}
			},
		},
		{
			name: "for_enumerate",
			code: `
xs = ["a", "b", "c"]
out = []
for i, v in enumerate(xs):
    out.append([i, v])
out
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 3 {
					t.Errorf("result = %v, want 3-element list", result)
					return
				}
				first, ok := list[0].([]any)
				if !ok || len(first) != 2 {
					t.Errorf("list[0] = %v, want 2-element pair", list[0])
					return
				}
				if first[0] != float64(0) || first[1] != "a" {
					t.Errorf("list[0] = %v, want [0, a]", first)
				}
			},
		},
		{
			name: "early_return",
			code: `
def maybe(x):
    if x < 0:
        return "neg"
    if x == 0:
        return "zero"
    return "pos"

[maybe(-1), maybe(0), maybe(5)]
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 3 {
					t.Errorf("result = %v, want 3-element list", result)
					return
				}
				if list[0] != "neg" || list[1] != "zero" || list[2] != "pos" {
					t.Errorf("result = %v, want [neg zero pos]", list)
				}
			},
		},
		{
			name: "none_sentinel",
			code: `
def pick(x):
    return x if x is not None else "fallback"

[pick(None), pick("val")]
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 2 {
					t.Errorf("result = %v, want 2-element list", result)
					return
				}
				if list[0] != "fallback" {
					t.Errorf("list[0] = %v, want fallback", list[0])
				}
				if list[1] != "val" {
					t.Errorf("list[1] = %v, want val", list[1])
				}
			},
		},
		{
			name: "ternary",
			code: `
x = 5
"big" if x > 3 else "small"
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "big" {
					t.Errorf("result = %v, want big", result)
				}
			},
		},
		{
			name: "not_and_or",
			code: `
a = True
b = False
[not a, not b, a and b, a or b, (not b) and a]
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				list, ok := result.([]any)
				if !ok || len(list) != 5 {
					t.Errorf("result = %v, want 5-element list", result)
					return
				}
				want := []any{false, true, false, true, true}
				for i, w := range want {
					if list[i] != w {
						t.Errorf("list[%d] = %v, want %v", i, list[i], w)
					}
				}
			},
		},
		{
			name: "string_slice",
			code: `"hello world"[:5]`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				if result != "hello" {
					t.Errorf("result = %v, want hello", result)
				}
			},
		},
		{
			name: "nested_fn_calls",
			code: `
def double(x): return x * 2
def inc(x): return x + 1
inc(double(inc(3)))
`,
			verify: func(t *testing.T, result any, err error) {
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				// inc(3)=4; double=8; inc=9.
				if result != float64(9) {
					t.Errorf("result = %v, want 9", result)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := runner.Execute(ctx, c.code, nil)
			if err != nil {
				t.Logf("idiom %q: Execute error: %v", c.name, err)
			}
			c.verify(t, result, err)
		})
	}
}

// contains is a tiny substring helper to avoid pulling in strings just for
// these tests' classify/assertion bits. Mirrors strings.Contains behavior.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
