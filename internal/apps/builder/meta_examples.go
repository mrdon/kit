// Package builder: meta_examples.go implements the `builder_examples`
// meta-tool — admins call this to discover curated starter app bundles
// they can paste into Claude Code and materialise via create_app /
// app_create_script / app_expose_tool. Admin-only; tenant-
// agnostic (the payloads are static templates, not DB reads).
//
// Contract:
//
//	builder_examples()           -> [ {id, title, description}, ... ]
//	builder_examples(name="...") -> { id, title, description, apps: [...] }
//
// Each example is fully self-describing: the admin's LLM harness reads the
// payload, then replays it through the other meta-tools to get a real,
// running bundle. We keep the definitions inside a Go map (examplesByID)
// so future tweaks are one source of truth — tests and runtime read the
// same bytes.
//
// Why not store examples in the DB: they're platform-curated starter
// content, not tenant data. Baking them into the binary means no
// migration per iteration and no cross-tenant leak risk. If we ever grow
// tenant-custom examples the two surfaces can coexist (DB first, then
// fall back to built-ins).
package builder

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mrdon/kit/internal/services"
)

// metaExampleTools enumerates the single Phase 5 examples meta-tool.
// Admin-only: starter scripts sometimes reference decisions, briefings,
// and cross-app tools a non-admin shouldn't be inspecting.
var metaExampleTools = []services.ToolMeta{
	{
		Name:        "builder_examples",
		Description: "Browse curated starter examples for the builder substrate. Without args, returns a list of {id, title, description}. With name='...', returns the full example definition so the admin can paste it through create_app + app_create_script + app_expose_tool.",
		Schema: services.Props(map[string]any{
			"name": services.Field("string", "Optional example id (e.g. 'mug_club'). If omitted, returns the catalog."),
		}),
		AdminOnly: true,
	},
}

// MetaExampleTools exposes the examples meta-tool so App.ToolMetas can
// fold it into the combined catalog.
func MetaExampleTools() []services.ToolMeta { return metaExampleTools }

// metaExampleAgentHandler resolves a handler by tool name. Nil for
// unknown names so the registration loop short-circuits.
func metaExampleAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	if name == "builder_examples" {
		return handleBuilderExamples
	}
	return nil
}

// exampleCatalogEntry is the summary row returned when the tool is
// called without a name. The LLM picks an ID from this list and calls
// back with name=... for the full definition.
type exampleCatalogEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// exampleAppSpec describes one builder_apps bundle inside an example.
// Multiple entries in Apps let future examples stitch several apps —
// the current starter set all happen to use a single app each.
type exampleAppSpec struct {
	AppName  string                `json:"app_name"`
	Scripts  []exampleScriptSpec   `json:"scripts"`
	Expose   []exampleExposeSpec   `json:"expose,omitempty"`
	Schedule []exampleScheduleSpec `json:"schedule,omitempty"`
}

type exampleScriptSpec struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type exampleExposeSpec struct {
	Script         string         `json:"script"`
	Fn             string         `json:"fn"`
	ToolName       string         `json:"tool_name"`
	VisibleToRoles []string       `json:"visible_to_roles"`
	ArgsSchema     map[string]any `json:"args_schema"`
}

type exampleScheduleSpec struct {
	Script string `json:"script"`
	Fn     string `json:"fn"`
	Cron   string `json:"cron"`
}

// exampleDefinition is the full payload returned when the caller asks
// for a specific example by ID. `Apps` is a slice so the shape is
// stable across single- and multi-app examples.
type exampleDefinition struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Apps        []exampleAppSpec `json:"apps"`
}

// handleBuilderExamples is the thin dispatcher: no args -> catalog,
// name arg -> detailed definition, unknown name -> clean error.
func handleBuilderExamples(ec *execContextLike, input json.RawMessage) (string, error) {
	if err := guardAdmin(ec.Caller); err != nil {
		return "", err
	}
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	name, err := argOptionalString(m, "name")
	if err != nil {
		return "", err
	}
	if name == "" {
		return formatToolResult(exampleCatalog())
	}
	def, ok := examplesByID[name]
	if !ok {
		return "", fmt.Errorf("unknown example %q (call builder_examples with no args to list available IDs)", name)
	}
	return formatToolResult(def)
}

// exampleCatalog returns the summary rows sorted by ID so the output is
// deterministic across process restarts — handy for LLM caching and
// for tests asserting on the slice shape.
func exampleCatalog() []exampleCatalogEntry {
	out := make([]exampleCatalogEntry, 0, len(examplesByID))
	for _, ex := range examplesByID {
		out = append(out, exampleCatalogEntry{
			ID:          ex.ID,
			Title:       ex.Title,
			Description: ex.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// examplesByID is the source of truth. Adding or editing an example
// means one map entry + (if tests care) a catalog length bump.
var examplesByID = map[string]exampleDefinition{
	"mug_club":                 mugClubExample(),
	"crm_with_service_layer":   crmExample(),
	"review_triage":            reviewTriageExample(),
	"vendor_book_multi_script": vendorBookExample(),
	"weekly_digest":            weeklyDigestExample(),
	"timecards":                timecardsExample(),
}

// Individual example constructors live in the helper functions below.
// Splitting one per function keeps each script body at a readable
// length and keeps this dispatcher file under the 500-LOC cap.

func mugClubExample() exampleDefinition {
	body := "" +
		"def add_member(name, email, tier=\"silver\"):\n" +
		"    return db_insert_one(\"members\", {\n" +
		"        \"name\": name,\n" +
		"        \"email\": email.lower(),\n" +
		"        \"tier\": tier,\n" +
		"    })\n" +
		"\n" +
		"def list_members(tier=None, limit=50):\n" +
		"    filter = {\"tier\": tier} if tier else {}\n" +
		"    return db_find(\"members\", filter, limit=limit, sort=[(\"_created_at\", -1)])\n" +
		"\n" +
		"def update_tier(member_id, tier):\n" +
		"    db_update_one(\"members\", {\"_id\": member_id}, {\"$set\": {\"tier\": tier}})\n" +
		"    return {\"ok\": True}\n"
	return exampleDefinition{
		ID:          "mug_club",
		Title:       "Mug club — day-1 CRUD smoke test",
		Description: "Single-script app with insert/find/update. The simplest working example; use it to verify your builder plumbing end-to-end.",
		Apps: []exampleAppSpec{{
			AppName: "mug_club",
			Scripts: []exampleScriptSpec{{Name: "core", Body: body}},
			Expose: []exampleExposeSpec{
				{
					Script: "core", Fn: "add_member", ToolName: "add_mug_member",
					VisibleToRoles: []string{"manager", "bartender"},
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":  map[string]any{"type": "string", "description": "Member's display name"},
							"email": map[string]any{"type": "string", "description": "Email address (stored lowercased)"},
							"tier":  map[string]any{"type": "string", "description": "Membership tier. Defaults to silver."},
						},
						"required": []string{"name", "email"},
					},
				},
				{
					Script: "core", Fn: "list_members", ToolName: "list_mug_members",
					VisibleToRoles: []string{"manager", "bartender"},
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tier":  map[string]any{"type": "string", "description": "Optional tier filter"},
							"limit": map[string]any{"type": "integer", "description": "Max rows to return (default 50)"},
						},
					},
				},
				{
					Script: "core", Fn: "update_tier", ToolName: "update_mug_tier",
					VisibleToRoles: []string{"manager", "bartender"},
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"member_id": map[string]any{"type": "string", "description": "Member _id to update"},
							"tier":      map[string]any{"type": "string", "description": "New tier label"},
						},
						"required": []string{"member_id", "tier"},
					},
				},
			},
		}},
	}
}

func crmExample() exampleDefinition {
	utils := "" +
		"def format_phone(raw):\n" +
		"    digits = \"\".join(c for c in raw if c.isdigit())\n" +
		"    if len(digits) == 10:\n" +
		"        return \"(\" + digits[0:3] + \") \" + digits[3:6] + \"-\" + digits[6:10]\n" +
		"    return raw\n" +
		"\n" +
		"def validate_email(email):\n" +
		"    if \"@\" not in email:\n" +
		"        return False, \"email missing @\"\n" +
		"    if \".\" not in email.split(\"@\")[-1]:\n" +
		"        return False, \"email domain invalid\"\n" +
		"    return True, None\n"
	main := "" +
		"def add_contact(name, email, phone=None):\n" +
		"    ok, err = shared(\"utils\", \"validate_email\", email=email)\n" +
		"    if not ok:\n" +
		"        return {\"error\": err}\n" +
		"    doc = {\"name\": name, \"email\": email.lower()}\n" +
		"    if phone:\n" +
		"        doc[\"phone\"] = shared(\"utils\", \"format_phone\", raw=phone)\n" +
		"    return db_insert_one(\"contacts\", doc)\n" +
		"\n" +
		"def find_contact(name):\n" +
		"    hits = db_find(\"contacts\", {\"name\": name}, limit=1)\n" +
		"    return hits[0] if hits else None\n" +
		"\n" +
		"def add_note(contact_id, note):\n" +
		"    db_update_one(\"contacts\", {\"_id\": contact_id}, {\"$push\": {\"notes\": {\"text\": note, \"at\": now()}}})\n" +
		"    return {\"ok\": True}\n"
	roles := []string{"manager", "sales"} // admins see these via superuser bypass
	return exampleDefinition{
		ID:          "crm_with_service_layer",
		Title:       "CRM with a shared service layer",
		Description: "Two scripts — `utils` holds validation/formatting helpers, `main` is business logic calling them via shared(). Demonstrates the service-layer pattern.",
		Apps: []exampleAppSpec{{
			AppName: "crm",
			Scripts: []exampleScriptSpec{
				{Name: "utils", Body: utils},
				{Name: "main", Body: main},
			},
			Expose: []exampleExposeSpec{
				{
					Script: "main", Fn: "add_contact", ToolName: "crm_add_contact",
					VisibleToRoles: roles,
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":  map[string]any{"type": "string", "description": "Contact name"},
							"email": map[string]any{"type": "string", "description": "Email address"},
							"phone": map[string]any{"type": "string", "description": "Optional phone number (reformatted on store)"},
						},
						"required": []string{"name", "email"},
					},
				},
				{
					Script: "main", Fn: "find_contact", ToolName: "crm_lookup",
					VisibleToRoles: roles,
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string", "description": "Contact name to look up"},
						},
						"required": []string{"name"},
					},
				},
				{
					Script: "main", Fn: "add_note", ToolName: "crm_add_note",
					VisibleToRoles: roles,
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"contact_id": map[string]any{"type": "string", "description": "Contact _id"},
							"note":       map[string]any{"type": "string", "description": "Note text to append"},
						},
						"required": []string{"contact_id", "note"},
					},
				},
			},
		}},
	}
}

func reviewTriageExample() exampleDefinition {
	body := "" +
		"def triage():\n" +
		"    pending = db_find(\"reviews\", {\"triaged\": False}, limit=50)\n" +
		"    for r in pending:\n" +
		"        cat = llm_classify(\n" +
		"            text=r[\"text\"],\n" +
		"            categories=[\"complaint\", \"praise\", \"suggestion\", \"noise\"],\n" +
		"        )\n" +
		"        db_update_one(\"reviews\", {\"_id\": r[\"_id\"]}, {\"$set\": {\"category\": cat, \"triaged\": True}})\n" +
		"        if cat == \"complaint\":\n" +
		"            draft = llm_generate(\n" +
		"                prompt=\"Draft an empathetic 2-sentence reply to: \" + r[\"text\"],\n" +
		"                max_tokens=160,\n" +
		"            )\n" +
		"            create_decision(\n" +
		"                title=\"Respond to negative review?\",\n" +
		"                body=r[\"text\"][:300],\n" +
		"                options=[\n" +
		"                    {\"label\": \"Post reply\", \"prompt\": draft},\n" +
		"                    {\"label\": \"Ignore\", \"prompt\": \"\"},\n" +
		"                ],\n" +
		"                priority=\"medium\",\n" +
		"            )\n" +
		"\n" +
		"def list_recent_complaints(limit=20):\n" +
		"    return db_find(\"reviews\", {\"category\": \"complaint\"}, limit=limit, sort=[(\"_created_at\", -1)])\n"
	return exampleDefinition{
		ID:          "review_triage",
		Title:       "Review triage — scheduled LLM + decisions",
		Description: "Cron-driven triage of incoming reviews: llm_classify labels each row, complaints spawn a decision with an LLM-drafted reply option. Exposes a read tool for humans.",
		Apps: []exampleAppSpec{{
			AppName: "review_triage",
			Scripts: []exampleScriptSpec{{Name: "main", Body: body}},
			Expose: []exampleExposeSpec{
				{
					Script: "main", Fn: "list_recent_complaints", ToolName: "recent_complaints",
					VisibleToRoles: []string{"manager"},
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"limit": map[string]any{"type": "integer", "description": "Max rows to return (default 20)"},
						},
					},
				},
			},
			Schedule: []exampleScheduleSpec{
				{Script: "main", Fn: "triage", Cron: "0 9 * * *"},
			},
		}},
	}
}

func vendorBookExample() exampleDefinition {
	validators := "" +
		"def valid_contact(doc):\n" +
		"    errs = []\n" +
		"    if not doc.get(\"name\"):\n" +
		"        errs.append(\"name required\")\n" +
		"    if not doc.get(\"email\"):\n" +
		"        errs.append(\"email required\")\n" +
		"    return errs\n"
	main := "" +
		"def add_vendor(name, email, specialty):\n" +
		"    errs = shared(\"validators\", \"valid_contact\", doc={\"name\": name, \"email\": email})\n" +
		"    if errs:\n" +
		"        return {\"ok\": False, \"errors\": errs}\n" +
		"    doc = db_insert_one(\"vendors\", {\"name\": name, \"email\": email.lower(), \"specialty\": specialty})\n" +
		"    return {\"ok\": True, \"vendor\": doc}\n" +
		"\n" +
		"def list_vendors(specialty=None):\n" +
		"    filter = {\"specialty\": specialty} if specialty else {}\n" +
		"    return db_find(\"vendors\", filter, sort=[(\"name\", 1)], limit=100)\n"
	roles := []string{"manager"} // admins see these via superuser bypass
	return exampleDefinition{
		ID:          "vendor_book_multi_script",
		Title:       "Vendor book — cross-script validators",
		Description: "Two scripts: `validators` holds pure form-style checks, `main` uses shared() to reuse them. Shows the minimum useful multi-script pattern.",
		Apps: []exampleAppSpec{{
			AppName: "vendor_book",
			Scripts: []exampleScriptSpec{
				{Name: "validators", Body: validators},
				{Name: "main", Body: main},
			},
			Expose: []exampleExposeSpec{
				{
					Script: "main", Fn: "add_vendor", ToolName: "vendor_add",
					VisibleToRoles: roles,
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":      map[string]any{"type": "string", "description": "Vendor name"},
							"email":     map[string]any{"type": "string", "description": "Contact email"},
							"specialty": map[string]any{"type": "string", "description": "Vendor specialty category"},
						},
						"required": []string{"name", "email", "specialty"},
					},
				},
				{
					Script: "main", Fn: "list_vendors", ToolName: "vendor_list",
					VisibleToRoles: roles,
					ArgsSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"specialty": map[string]any{"type": "string", "description": "Optional specialty filter"},
						},
					},
				},
			},
		}},
	}
}

func weeklyDigestExample() exampleDefinition {
	body := "" +
		"def compile():\n" +
		"    # Roll up mug club growth this week via the exposed tool\n" +
		"    members = tools_call(\"list_mug_members\", {})\n" +
		"    complaints = tools_call(\"recent_complaints\", {\"limit\": 10})\n" +
		"    body = \"Weekly digest: \" + str(len(members)) + \" members, \" + str(len(complaints)) + \" complaints this week.\"\n" +
		"    create_briefing(title=\"Weekly digest\", body=body, severity=\"info\")\n" +
		"    return {\"ok\": True}\n"
	return exampleDefinition{
		ID:          "weekly_digest",
		Title:       "Weekly digest — scheduled cross-app composition",
		Description: "Cron job that composes exposed tools from other apps (mug_club, review_triage) into a single briefing. Depends on those examples being installed.",
		Apps: []exampleAppSpec{{
			AppName: "weekly_digest",
			Scripts: []exampleScriptSpec{{Name: "main", Body: body}},
			Schedule: []exampleScheduleSpec{
				{Script: "main", Fn: "compile", Cron: "0 8 * * MON"},
			},
		}},
	}
}
