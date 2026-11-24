package ledger

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAccuracy(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id string, done, total int, forecastP90, laterCost int64, conf string) {
		l.StartRun(Run{ID: id, Started: time.Now()})
		l.Record(Call{RunID: id, CostMicro: 1_000_000, At: time.Now()}) // spent before forecast
		l.SetProgress(id, done, total)
		l.SaveForecast(id, 1_000_000, forecastP90/2, forecastP90, conf, done, total)
		l.Record(Call{RunID: id, CostMicro: laterCost, At: time.Now()})
	}
	mk("hit", 8, 8, 4_000_000, 3_000_000, "medium")  // 3M after <= p90 4M
	mk("miss", 8, 8, 4_000_000, 5_000_000, "medium") // 5M after > 4M
	mk("lowconf", 8, 8, 4_000_000, 1, "low")         // not scoreable
	mk("stopped", 2, 8, 4_000_000, 1, "medium")      // not finished
	for _, id := range []string{"hit", "miss", "lowconf", "stopped"} {
		l.EndRun(id)
	}
	a, _ := l.Accuracy()
	if a.Scored != 2 || a.Hits != 1 {
		t.Fatalf("%+v", a)
	}
	if o, _ := l.EndRun("stopped"); o != "stopped" {
		t.Fatal(o)
	}
}
