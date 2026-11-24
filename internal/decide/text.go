package decide

import (
	"fmt"
	"strings"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
)

// USD formats micro-dollars for display only. Money is never computed from this.
func USD(m int64) string { return money.Format(m) }

// Text is the card as plain text: the same words in the terminal, the desktop
// notification and Telegram. Numbers and step counts only; never prompts or code.
func (c Card) Text() string {
	var b strings.Builder
	switch {
	case c.Reason == "cap":
		fmt.Fprintf(&b, "Budget reached on run %s: %s of %s used.\n", c.Run, USD(c.Spent), USD(c.Budget))
	case c.Method == "rate":
		fmt.Fprintf(&b, "Run %s will likely not fit its budget.\nNo plan seen, so this is from its spending rate. %s of %s used.\nLikely %s to %s more (%s confidence).\n",
			c.Run, USD(c.Spent), USD(c.Budget), USD(c.P10), USD(c.P90), c.Confidence)
	default:
		fmt.Fprintf(&b, "Run %s will not fit its budget.\n%d of %d steps done, %s of %s used.\nLikely %s to %s more to finish (%s confidence).\n",
			c.Run, c.Done, c.Total, USD(c.Spent), USD(c.Budget), USD(c.P10), USD(c.P90), c.Confidence)
	}
	if len(c.LastActions) > 0 {
		fmt.Fprintf(&b, "Last actions: %s.\n", strings.Join(c.LastActions, ", "))
	}
	return b.String()
}
