package caps

import (
	"errors"
	"testing"
	"time"
)

type fake struct {
	run, since int64
	budget     map[string]int64
}

func (f fake) SpentRun(string) (int64, error)      { return f.run, nil }
func (f fake) SpentSince(time.Time) (int64, error) { return f.since, nil }
func (f fake) RunBudget(r string) (int64, bool)    { b, ok := f.budget[r]; return b, ok }
func (f fake) SetRunBudget(r string, m int64) error {
	f.budget[r] = m
	return nil
}

func TestRunCapStopsBeforeOvershoot(t *testing.T) {
	g := New(Limits{Run: 500_000}, fake{run: 400_000, budget: map[string]int64{}})
	if err := g.Reserve("r", 100_000); err != nil {
		t.Fatal(err)
	}
	var d *Denied
	if err := g.Reserve("r", 1); !errors.As(err, &d) || d.Scope != "run" {
		t.Fatalf("want run denial, got %v", err)
	}
	g.Release("r", 100_000)
	if err := g.Reserve("r", 100_000); err != nil {
		t.Fatal("release must free budget")
	}
}

func TestSharedDayCapAcrossRuns(t *testing.T) {
	g := New(Limits{Day: 1_000_000}, fake{since: 0, budget: map[string]int64{}})
	if err := g.Reserve("a", 600_000); err != nil {
		t.Fatal(err)
	}
	if err := g.Reserve("b", 600_000); err == nil {
		t.Fatal("two runs must not jointly overshoot day cap")
	}
}

func TestRaisedBudgetIsStored(t *testing.T) {
	store := fake{run: 500_000, budget: map[string]int64{}}
	g := New(Limits{Run: 500_000}, store)
	if err := g.Reserve("r", 1); err == nil {
		t.Fatal("cap reached")
	}
	g.Raise("r", 1_000_000)
	if store.budget["r"] != 1_500_000 {
		t.Fatalf("raise not stored: %v", store.budget)
	}
	restarted := New(Limits{Run: 500_000}, store) // a new proxy process reads the same ledger
	if restarted.RunCap("r") != 1_500_000 || restarted.Reserve("r", 1) != nil {
		t.Fatal("raised budget lost on restart")
	}
}
