package decide

import (
	"fmt"
	"strings"
)

// USD formats micro-dollars for display only. Money is never computed from this.
func USD(m int64) string { return fmt.Sprintf("$%.2f", float64(m)/1e6) }

// Text is the card as plain text: the same words in the terminal, the desktop
// notification and Telegram. Numbers and step counts only; never prompts or code.
func (c Card) Text() string {
	var b strings.Builder
	if c.Reason == "cap" {
		fmt.Fprintf(&b, "Budget reached on run %s: %s of %s used.\n", c.Run, USD(c.Spent), USD(c.Budget))
	} else {
		fmt.Fprintf(&b, "Run %s will not fit its budget.\n%d of %d steps done, %s of %s used.\nLikely %s to %s more to finish (%s confidence).\n",
			c.Run, c.Done, c.Total, USD(c.Spent), USD(c.Budget), USD(c.P10), USD(c.P90), c.Confidence)
	}
	return b.String()
}
