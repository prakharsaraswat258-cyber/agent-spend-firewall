package caps

import (
	"errors"
	"testing"
	"time"
)

type fake struct{ run, since int64 }

func (f fake) SpentRun(string) (int64, error)      { return f.run, nil }
func (f fake) SpentSince(time.Time) (int64, error) { return f.since, nil }

func TestRunCapStopsBeforeOvershoot(t *testing.T) {
	g := New(Limits{Run: 500_000}, fake{run: 400_000})
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
	g := New(Limits{Day: 1_000_000}, fake{since: 0})
	if err := g.Reserve("a", 600_000); err != nil {
		t.Fatal(err)
	}
	if err := g.Reserve("b", 600_000); err == nil {
		t.Fatal("two runs must not jointly overshoot day cap")
	}
}
