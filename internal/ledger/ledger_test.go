package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/forecast"
)

func TestAccuracy(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// Range shown: P10 = 1M, P50 = 2M, P90 = 4M.
	mk := func(id string, done, total int, laterCost int64, conf string) {
		l.StartRun(Run{ID: id, Started: time.Now()})
		l.Record(Call{RunID: id, CostMicro: 1_000_000, At: time.Now()}) // spent before forecast
		l.SetProgress(id, done, total)
		l.SaveForecast(id, 1_000_000, forecast.Forecast{HasPlan: true, P10: 1_000_000, P50: 2_000_000, P90: 4_000_000, Confidence: conf}, done, total)
		l.Record(Call{RunID: id, CostMicro: laterCost, At: time.Now()})
	}
	mk("hit", 8, 8, 3_000_000, "medium")  // 3M inside 1M-4M
	mk("over", 8, 8, 5_000_000, "medium") // 5M above the range
	mk("under", 8, 8, 200_000, "medium")  // forecast far too high: a miss, not a "right"
	mk("lowconf", 8, 8, 1, "low")         // not scoreable
	mk("stopped", 2, 8, 1, "medium")      // not finished
	for _, id := range []string{"hit", "over", "under", "lowconf", "stopped"} {
		l.EndRun(id)
	}
	a, _ := l.Accuracy()
	if a.Scored != 3 || a.Hits != 1 || a.Over != 1 || a.Under != 1 {
		t.Fatalf("%+v", a)
	}
	if o, _ := l.EndRun("stopped"); o != "stopped" {
		t.Fatal(o)
	}
}

func TestBandCalibratesFromFinishedRuns(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if b := l.Band(); b != forecast.DefaultBand {
		t.Fatalf("no history: %+v", b)
	}
	// Five runs that each cost 3x their p50: the band must widen upward.
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		l.StartRun(Run{ID: id, Started: time.Now().Add(time.Duration(i) * time.Second)})
		l.SaveForecast(id, 0, forecast.Forecast{HasPlan: true, P10: 500_000, P50: 1_000_000, P90: 2_000_000, Confidence: "medium"}, 3, 3)
		l.Record(Call{RunID: id, CostMicro: 3_000_000, At: time.Now()})
		l.SetProgress(id, 3, 3)
		l.EndRun(id)
	}
	if b := l.Band(); b.Hi < 3 {
		t.Fatalf("band did not learn: %+v", b)
	}
}
