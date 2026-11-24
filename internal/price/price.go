// Package price computes exact call cost in micro-dollars from a versioned table.
package price

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

//go:embed prices.json
var embedded []byte

// Micro is USD * 1e6. Integer so sums never drift.
type Micro = int64

type Entry struct {
	Provider   string  `json:"provider"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
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

// Lookup matches exact name, else the longest key that prefixes model.
func (t *Table) Lookup(model string) (Entry, error) {
	if e, ok := t.Models[model]; ok {
		return e, nil
	}
	best := ""
	for k := range t.Models {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return Entry{}, fmt.Errorf("unknown model %q: add it to prices.json (price table %s)", model, t.Version)
	}
	return t.Models[best], nil
}

// Cost: tokens * ($/Mtok) is exactly micro-dollars. Rounded once per call.
func (e Entry) Cost(u Usage) Micro {
	f := float64(u.Input)*e.Input + float64(u.Output)*e.Output +
		float64(u.CacheRead)*e.CacheRead + float64(u.CacheWrite)*e.CacheWrite
	return Micro(math.Round(f))
}

// MaxCost is the worst case for a call: all input at the highest input rate, full max output.
func (e Entry) MaxCost(inputTokens, maxOutput int64) Micro {
	rate := math.Max(e.Input, math.Max(e.CacheRead, e.CacheWrite))
	return Micro(math.Ceil(float64(inputTokens)*rate + float64(maxOutput)*e.Output))
}
