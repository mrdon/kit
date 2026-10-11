package anthropic

import "strings"

// List prices per million tokens, used only to put a rough dollar figure
// next to a chat turn so people can see what a request cost. Keep these
// in step with the published price list; an unknown family estimates
// nothing rather than guessing. Cache writes bill at 1.25x input and
// cache reads at 0.1x, per the API's prompt-caching pricing.
type price struct{ input, output float64 }

var prices = map[string]price{
	"haiku":  {1, 5},
	"sonnet": {3, 15},
	"opus":   {5, 25},
}

// EstimateCost prices one usage record for a model. ok is false when the
// model's family is not in the table.
func EstimateCost(model string, u Usage) (usd float64, ok bool) {
	var p price
	switch {
	case strings.Contains(model, "haiku"):
		p = prices["haiku"]
	case strings.Contains(model, "sonnet"):
		p = prices["sonnet"]
	case strings.Contains(model, "opus"):
		p = prices["opus"]
	default:
		return 0, false
	}
	perTok := 1e-6
	usd = float64(u.InputTokens)*p.input*perTok +
		float64(u.CacheCreationInputTokens)*p.input*1.25*perTok +
		float64(u.CacheReadInputTokens)*p.input*0.1*perTok +
		float64(u.OutputTokens)*p.output*perTok
	return usd, true
}

// Add accumulates another usage record.
func (u *Usage) Add(x Usage) {
	u.InputTokens += x.InputTokens
	u.OutputTokens += x.OutputTokens
	u.CacheCreationInputTokens += x.CacheCreationInputTokens
	u.CacheReadInputTokens += x.CacheReadInputTokens
}
