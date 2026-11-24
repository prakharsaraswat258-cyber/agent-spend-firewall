package price

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"
)

// StaleAfter is the age at which a price table gets a warning.
const StaleAfter = 30 * 24 * time.Hour

// Validate rejects a table that would mis-price calls: every model needs a
// positive input and output price, and no rate may be negative.
func (t *Table) Validate() error {
	if t == nil || len(t.Models) == 0 {
		return errors.New("price table has no models")
	}
	if _, ok := t.Date(); !ok {
		return fmt.Errorf("price table version %q does not start with a date (YYYY-MM-DD)", t.Version)
	}
	for name, e := range t.Models {
		if err := e.validate(); err != nil {
			return fmt.Errorf("model %s: %w", name, err)
		}
	}
	return nil
}

func (e Entry) validate() error {
	if e.Input <= 0 || e.Output <= 0 {
		return errors.New("input and output prices must be above 0")
	}
	if e.CacheRead < 0 || e.CacheWrite < 0 || e.CacheWrite1h < 0 || e.LongAbove < 0 {
		return errors.New("prices cannot be negative")
	}
	if e.Long != nil {
		if e.LongAbove == 0 {
			return errors.New("long-context prices need long_above")
		}
		if e.Long.Long != nil {
			return errors.New("long-context prices cannot nest")
		}
		return e.Long.validate()
	}
	return nil
}

// Date is the day the table was read from the pricing pages (its version prefix).
func (t *Table) Date() (time.Time, bool) {
	if len(t.Version) < 10 {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", t.Version[:10])
	return d, err == nil
}

// Stale reports whether the table is older than StaleAfter at now.
func (t *Table) Stale(now time.Time) bool {
	d, ok := t.Date()
	return !ok || now.Sub(d) > StaleAfter
}

// Newer reports whether version a is later than version b ("2026-11-24b" > "2026-11-24").
func Newer(a, b string) bool { return a > b }

// WithCustom returns a copy of t with the user's own entries (from `pitstop prices add`)
// on top. Custom entries win over the table's.
func (t *Table) WithCustom(custom map[string]Entry) *Table {
	out := &Table{Version: t.Version, Models: maps.Clone(t.Models)}
	if out.Models == nil {
		out.Models = map[string]Entry{}
	}
	for k, v := range custom {
		out.Models[k] = v
	}
	if len(custom) > 0 {
		out.Version += "+custom"
	}
	return out
}

// Change is one model whose price differs between two tables.
type Change struct {
	Model      string
	Old, New   *Entry // nil when added or removed
	BigDropPct int    // largest drop of input or output price, in percent (0 if none)
}

// Diff lists what changes from old to new, sorted by model.
func Diff(old, new *Table) []Change {
	var out []Change
	for name, ne := range new.Models {
		oe, ok := old.Models[name]
		switch {
		case !ok:
			n := ne
			out = append(out, Change{Model: name, New: &n})
		case !sameEntry(oe, ne):
			o, n := oe, ne
			out = append(out, Change{Model: name, Old: &o, New: &n, BigDropPct: max(dropPct(oe.Input, ne.Input), dropPct(oe.Output, ne.Output))})
		}
	}
	for name, oe := range old.Models {
		if _, ok := new.Models[name]; !ok {
			o := oe
			out = append(out, Change{Model: name, Old: &o})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func sameEntry(a, b Entry) bool {
	if a.Input != b.Input || a.Output != b.Output || a.CacheRead != b.CacheRead || a.CacheWrite != b.CacheWrite ||
		a.CacheWrite1h != b.CacheWrite1h || a.LongAbove != b.LongAbove || (a.Long == nil) != (b.Long == nil) {
		return false
	}
	return a.Long == nil || sameEntry(*a.Long, *b.Long)
}

func dropPct(old, new float64) int {
	if old <= 0 || new >= old {
		return 0
	}
	return int((old - new) / old * 100)
}

// String is one line per change, for `pitstop prices update`.
func (c Change) String() string {
	switch {
	case c.Old == nil:
		return fmt.Sprintf("+ %s: $%g in / $%g out", c.Model, c.New.Input, c.New.Output)
	case c.New == nil:
		return fmt.Sprintf("- %s (removed: calls to it will be blocked until you add a price)", c.Model)
	default:
		s := fmt.Sprintf("~ %s: $%g/$%g -> $%g/$%g (in/out per MTok)", c.Model, c.Old.Input, c.Old.Output, c.New.Input, c.New.Output)
		if c.BigDropPct >= 50 {
			s += fmt.Sprintf("  [drop %d%%]", c.BigDropPct)
		}
		return s
	}
}

// Summary is a short count of changes.
func Summary(cs []Change) string {
	var add, rm, ch int
	for _, c := range cs {
		switch {
		case c.Old == nil:
			add++
		case c.New == nil:
			rm++
		default:
			ch++
		}
	}
	parts := []string{}
	for _, p := range []struct {
		n int
		w string
	}{{add, "added"}, {ch, "changed"}, {rm, "removed"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.w))
		}
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}
