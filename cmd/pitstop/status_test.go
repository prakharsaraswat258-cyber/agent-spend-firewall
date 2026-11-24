package main

import (
	"path/filepath"
	"testing"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/caps"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/ledger"
)

func TestStatusUsesTheCapsServeRuns(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cfg := Config{BudgetUSD: 5}
	if b := runBudget(cfg, l, "r"); b != 5_000_000 {
		t.Fatalf("config fallback %d", b)
	}
	l.SetSetting("run_cap", 2_000_000) // pitstop serve --run-cap 2
	if b := runBudget(cfg, l, "r"); b != 2_000_000 {
		t.Fatalf("serve cap %d", b)
	}
	l.SetRunBudget("r", 3_500_000) // raised on a decision card
	if b := runBudget(cfg, l, "r"); b != 3_500_000 {
		t.Fatalf("raised budget %d", b)
	}
	if b := runBudget(cfg, l, caps.Untagged+"20261124"); b != 0 {
		t.Fatalf("untagged run has no run cap, got %d", b)
	}
}

func TestFitLabel(t *testing.T) {
	f := forecast.Forecast{Method: "plan", P50: 2_000_000, P90: 4_000_000}
	for _, c := range []struct {
		spent, budget int64
		want          string
	}{{1_000_000, 10_000_000, "on track"}, {7_000_000, 10_000_000, "tight"}, {8_500_000, 10_000_000, "WON'T FIT BUDGET"}, {1, 0, "no run cap"}} {
		if got := fitLabel(c.spent, c.budget, f); got != c.want {
			t.Errorf("spent %d budget %d: %q want %q", c.spent, c.budget, got, c.want)
		}
	}
}
