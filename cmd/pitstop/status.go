package main

import (
	"fmt"
	"os"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

func usd(m int64) string { return fmt.Sprintf("$%.2f", float64(m)/1e6) }

// statusCmd prints one line, usable as a Claude Code status line:
//
//	$3.20 / $10 · 5/8 steps · likely $5.10-$10.20 more · low confidence
//
// With no run argument it uses the newest run.
func statusCmd(cfg Config, args []string) int {
	l, err := ledger.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()
	id := ""
	if len(args) > 0 {
		id = args[0]
	} else if rs, _ := l.Runs(1); len(rs) > 0 {
		id = rs[0].ID
	}
	if id == "" {
		fmt.Println("pitstop: no runs yet")
		return 0
	}
	r, err := l.GetRun(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run %q not found\n", id)
		return 1
	}
	spent, _ := l.SpentRun(id)
	budget := int64(cfg.BudgetUSD * 1e6)
	out := fmt.Sprintf("%s / %s", usd(spent), usd(budget))
	if r.StepsTotal == 0 {
		fmt.Println(out + " · no plan seen, forecast unavailable")
		return 0
	}
	costs, _ := l.CallCosts(id)
	f := forecast.Compute(spent, costs, r.StepsDone, r.StepsTotal)
	out += fmt.Sprintf(" · %d/%d steps · likely %s-%s more", r.StepsDone, r.StepsTotal, usd(f.P50), usd(f.P90))
	if forecast.WillOverrun(spent, budget, f) {
		out += " · WON'T FIT BUDGET"
	} else {
		out += " · on track"
	}
	fmt.Println(out + " · " + f.Confidence + " confidence")
	return 0
}
