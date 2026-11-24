// Package caps enforces hard spend limits with pre-call reservation,
// so concurrent runs cannot jointly overshoot a cap.
package caps

import (
	"fmt"
	"sync"
	"time"
)

// Spent is the ledger view the caps need. Run budgets raised by a human live
// there too, so they survive a proxy restart.
type Spent interface {
	SpentRun(run string) (int64, error)
	SpentSince(t time.Time) (int64, error)
	RunBudget(run string) (int64, bool)
	SetRunBudget(run string, micro int64) error
}

// Limits in micro-dollars; 0 means no limit.
type Limits struct{ Run, Day, Month int64 }

type Guard struct {
	mu       sync.Mutex
	L        Limits
	S        Spent
	Now      func() time.Time
	reserved map[string]int64 // run -> reserved
	total    int64
}

func New(l Limits, s Spent) *Guard {
	return &Guard{L: l, S: s, Now: time.Now, reserved: map[string]int64{}}
}

type Denied struct {
	Scope           string
	Cap, Used, Need int64
}

func (d *Denied) Error() string {
	return fmt.Sprintf("%s cap $%.4f would be exceeded: used+reserved $%.4f, call may cost up to $%.4f",
		d.Scope, usd(d.Cap), usd(d.Used), usd(d.Need))
}

func usd(m int64) float64 { return float64(m) / 1e6 }

// Reserve holds `need` for run. Caller must Release with the same amount.
func (g *Guard) Reserve(run string, need int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.Now() // local time: "per day" means the user's day
	checks := []struct {
		scope string
		cap   int64
		used  func() (int64, error)
	}{
		{"run", g.runCapLocked(run), func() (int64, error) { n, e := g.S.SpentRun(run); return n + g.reserved[run], e }},
		{"day", g.L.Day, func() (int64, error) {
			n, e := g.S.SpentSince(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
			return n + g.total, e
		}},
		{"month", g.L.Month, func() (int64, error) {
			n, e := g.S.SpentSince(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
			return n + g.total, e
		}},
	}
	for _, c := range checks {
		if c.cap == 0 {
			continue
		}
		used, err := c.used()
		if err != nil {
			return err // fail closed
		}
		if used+need > c.cap {
			return &Denied{c.scope, c.cap, used, need}
		}
	}
	g.reserved[run] += need
	g.total += need
	return nil
}

func (g *Guard) Release(run string, amt int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reserved[run] -= amt
	g.total -= amt
}

func (g *Guard) runCapLocked(run string) int64 {
	if c, ok := g.S.RunBudget(run); ok {
		return c
	}
	return g.L.Run
}

// RunCap is the run's current budget (0 = unlimited).
func (g *Guard) RunCap(run string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.runCapLocked(run)
}

// Raise adds budget to one run and stores it. Day and month caps stay owner-only: no API here.
func (g *Guard) Raise(run string, add int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c := g.runCapLocked(run); c > 0 && add > 0 {
		return g.S.SetRunBudget(run, c+add)
	}
	return nil
}
