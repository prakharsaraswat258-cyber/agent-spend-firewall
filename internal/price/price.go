// Package price computes exact call cost in micro-dollars from a versioned table.
package price

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

//go:embed prices.json
var embedded []byte

// Micro is USD * 1e6. Integer so sums never drift.
type Micro = int64

type Entry struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	// Long, when set, replaces these prices for prompts over LongAbove tokens.
	LongAbove int64  `json:"long_above"`
	Long      *Entry `json:"long"`
}

type Table struct {
	Version string           `json:"version"`
	Models  map[string]Entry `json:"models"`
}

// Usage is billable token counts. Input excludes cached tokens.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
}

func Load(b []byte) (*Table, error) {
	t := &Table{}
	if err := json.Unmarshal(b, t); err != nil {
		return nil, err
	}
	return t, nil
}

func Default() *Table {
	t, err := Load(embedded)
	if err != nil {
		panic(err)
	}
	return t
}

// dateSuffix is a dated snapshot of a model: -20250929 or -2024-07-18.
var dateSuffix = regexp.MustCompile(`^-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// Lookup matches the exact name, or the name plus a date suffix. Nothing looser:
// a newer model must never borrow an older model's price.
func (t *Table) Lookup(model string) (Entry, error) {
	if e, ok := t.Models[model]; ok {
		return e, nil
	}
	if i := strings.LastIndex(model, "-20"); i > 0 && dateSuffix.MatchString(model[i:]) {
		if e, ok := t.Models[model[:i]]; ok {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("unknown model %q: add it to prices.json (price table %s)", model, t.Version)
}

// Cost: tokens * ($/Mtok) is exactly micro-dollars. Rounded once per call.
func (e Entry) Cost(u Usage) Micro {
	if e.Long != nil && u.Input+u.CacheRead+u.CacheWrite > e.LongAbove {
		return e.Long.Cost(u)
	}
	f := float64(u.Input)*e.Input + float64(u.Output)*e.Output +
		float64(u.CacheRead)*e.CacheRead + float64(u.CacheWrite)*e.CacheWrite
	return Micro(math.Round(f))
}

// MaxCost is the worst case for a call: all input at the highest input rate, full max output.
func (e Entry) MaxCost(inputTokens, maxOutput int64) Micro {
	if e.Long != nil && inputTokens > e.LongAbove {
		return e.Long.MaxCost(inputTokens, maxOutput)
	}
	rate := math.Max(e.Input, math.Max(e.CacheRead, e.CacheWrite))
	return Micro(math.Ceil(float64(inputTokens)*rate + float64(maxOutput)*e.Output))
}
