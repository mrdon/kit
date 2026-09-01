package trivia

import "github.com/mrdon/kit/internal/services"

// Two tools, both READ-ONLY, and no SystemPrompt.
//
// Nothing about "pick cell 3, reveal now" is improved by routing through an
// LLM in a loud bar, and a mis-fired tool during a live game is destructive
// and unrecoverable — the kiosk app contributes zero tools for exactly this
// reason. But asking in Slack the next morning how it went is real value, and
// costs nothing to get wrong.
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
}
