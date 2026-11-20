package trivia

import "github.com/mrdon/kit/internal/services"

// The game tools are READ-ONLY, and there is no SystemPrompt.
//
// Nothing about "pick cell 3, reveal now" is improved by routing through an
// LLM in a loud bar, and a mis-fired tool during a live game is destructive
// and unrecoverable — the kiosk app contributes zero tools for exactly this
// reason. But asking in Slack the next morning how it went is real value, and
// costs nothing to get wrong.
//
// The bank tools are the exception, and a safe one: they never touch a game.
// trivia_import_pack goes through the same importQuestions the console upload
// uses, so a pack on a live board still cannot be replaced, and it refuses to
// overwrite a pack any night has already asked from (see coreImportPack).
//
// This slice is the single source of tool metadata. Both the agent registry
// and the MCP server build their surfaces from it, so a field added here
// appears on both by construction rather than by remembering to edit two
// switch statements.
var triviaTools = []services.ToolMeta{
	{
		Name: "trivia_status",
		Description: "Recent trivia games: which phase each is in, how many teams played, " +
			"and who is leading. Use it to answer 'how did last night go' or 'is a game running'.",
		Schema: services.Props(map[string]any{
			"limit": services.Field("integer", "How many games to list. Defaults to 5."),
		}),
	},
	{
		Name: "trivia_results",
		Description: "The final leaderboard and a per-round recap for one trivia game. " +
			"Name the game by its three-word name (e.g. 'brave-otter-lamp'), or omit it for the most recent.",
		Schema: services.Props(map[string]any{
			"game": services.Field("string", "The game's three-word name. Omit for the most recent game."),
		}),
	},
	{
		Name: "trivia_list_questions",
		Description: "The trivia question bank. Without questions=true it lists each pack (dataset) " +
			"with its question count, how many no game has asked yet, average difficulty and notes. " +
			"With questions=true it also lists every question: difficulty, prompt, answer and topics. " +
			"Use search to check a new pack for overlap, and the full listing to calibrate difficulty ratings.",
		Schema: services.Props(map[string]any{
			"pack":      services.Field("string", "Only this pack, by name. Omit for every pack."),
			"questions": services.Field("boolean", "List the questions too, not just the pack summaries. Defaults to false."),
			"search": services.Field("string", "Only questions whose text or topics contain one of these comma-separated terms "+
				"(case-insensitive), e.g. 'christmas, hanukkah, santa'. Lists the matching questions under their packs."),
		}),
	},
	{
		Name: "trivia_import_pack",
		Description: "Upload a trivia question pack from CSV text, the same import the console's " +
			"'Upload a set' runs. Header row required: question, topics (';'-separated), answer " +
			"(a plain number), and optionally difficulty (whole number 1-10). Other columns, " +
			"like source, are ignored. Bad rows are reported and skipped. A pack name that " +
			"already exists is refused unless replace=true, and a pack any game has already " +
			"asked from cannot be replaced here.",
		Schema: services.PropsReq(map[string]any{
			"name":    services.Field("string", "The pack's name, at most 60 characters."),
			"csv":     services.Field("string", "The whole CSV file, header row first."),
			"notes":   services.Field("string", "Optional note shown with the pack, e.g. where the questions came from."),
			"replace": services.Field("boolean", "Replace an existing, never-played pack of the same name. Defaults to false."),
		}, "name", "csv"),
	},
}
