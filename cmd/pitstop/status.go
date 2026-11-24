package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
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
	budget := runBudget(cfg, l, id)
	out := fmt.Sprintf("%s / %s", usd(spent), usd(budget))
	if budget == 0 {
		out = usd(spent) + " (no run cap)"
	}
	var f forecast.Forecast
	if r.StepsTotal > 0 {
		costs, _ := l.CallCosts(id)
		f = forecast.ComputeWith(l.Band(), spent, costs, r.StepsDone, r.StepsTotal)
		out += fmt.Sprintf(" · %d/%d steps", r.StepsDone, r.StepsTotal)
	} else {
		span, _ := l.ActiveSpan(id)
		f = forecast.Rate(l.Band(), spent, span, l.TypicalRunLength())
		out += " · no plan"
	}
	if !f.Valid() {
		fmt.Println(out + " · too early to forecast")
		return 0
	}
	out += fmt.Sprintf(" · likely %s-%s more", usd(f.P10), usd(f.P90))
	out += " · " + fitLabel(spent, budget, f)
	fmt.Println(out + " · " + f.Confidence + " confidence")
	return 0
}

// fitLabel: "WON'T FIT BUDGET" when the pause rule fires, "tight" when the likely
// cost fits but the high end does not, else "on track".
func fitLabel(spent, budget int64, f forecast.Forecast) string {
	switch {
	case budget <= 0:
		return "no run cap"
	case forecast.WillOverrun(spent, budget, f):
		return "WON'T FIT BUDGET"
	case spent+f.P90 > budget:
		return "tight"
	}
	return "on track"
}

// runBudget is the budget the proxy enforces for a run: a raise from a decision
// card, else the run cap `pitstop serve` was started with, else the config default.
func runBudget(cfg Config, l *ledger.Ledger, id string) int64 {
	if b, ok := l.RunBudget(id); ok {
		return b
	}
	if strings.HasPrefix(id, caps.Untagged) {
		return 0
	}
	if b, ok := l.Setting("run_cap"); ok {
		return b
	}
	return int64(cfg.BudgetUSD * 1e6)
}
