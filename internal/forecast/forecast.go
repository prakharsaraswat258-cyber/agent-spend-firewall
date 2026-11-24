// Package forecast estimates the cost to finish from plan progress.
// remaining ~ steps left x average cost per step so far x context-growth factor.
package forecast

import (
	"math"
	"sort"
)

type Forecast struct {
	HasPlan       bool
	P10, P50, P90 int64  // micro-dollars still to spend; shown as the range P10-P90
	Confidence    string // "low" until 3 steps are done, then "medium"
}

// Band turns the p50 into the shown range: P10 = Lo x P50, P90 = Hi x P50.
type Band struct{ Lo, Hi float64 }

// DefaultBand is used until enough finished runs exist to calibrate from.
var DefaultBand = Band{Lo: 0.5, Hi: 2}

// MinRuns is how many scored runs Calibrate needs before it trusts history.
const MinRuns = 5

// Compute uses the default band. costs are per-call costs in order; done/total are plan steps.
func Compute(spent int64, costs []int64, done, total int) Forecast {
	return ComputeWith(DefaultBand, spent, costs, done, total)
}

// ComputeWith forecasts with a calibrated band.
func ComputeWith(b Band, spent int64, costs []int64, done, total int) Forecast {
	if total <= 0 || done < 0 || done > total {
		return Forecast{Confidence: "low"}
	}
	f := Forecast{HasPlan: true, Confidence: "low"}
	if done >= 3 {
		f.Confidence = "medium"
	}
	left := total - done
	if done == 0 {
		// No finished step to average: assume one step costs what we spent so far.
		f.P50 = int64(left) * spent
	} else {
		f.P50 = int64(math.Round(float64(left) * float64(spent) / float64(done) * growth(costs)))
	}
	f.P10 = int64(math.Round(b.Lo * float64(f.P50)))
	f.P90 = int64(math.Round(b.Hi * float64(f.P50)))
	return f
}

// Calibrate sets the band from past finished runs. ratios are actual cost to
// finish divided by the p50 forecast at the scoring point. The band is the 10th
// to 90th percentile of those ratios, so about 8 in 10 runs land inside it.
// Clamped so a few odd runs can't produce a useless or collapsed range.
func Calibrate(ratios []float64) Band {
	if len(ratios) < MinRuns {
		return DefaultBand
	}
	r := append([]float64(nil), ratios...)
	sort.Float64s(r)
	q := func(p float64) float64 { return r[int(math.Round(p*float64(len(r)-1)))] }
	return Band{
		Lo: math.Min(0.9, math.Max(0.2, q(0.1))),
		Hi: math.Min(5, math.Max(1.1, q(0.9))),
	}
}

// growth: later calls cost more as context grows. Ratio of the last 5 calls'
// average to the overall average, clamped to [1, 2].
func growth(costs []int64) float64 {
	if len(costs) < 6 {
		return 1
	}
	var all, tail int64
	for _, c := range costs {
		all += c
	}
	for _, c := range costs[len(costs)-5:] {
		tail += c
	}
	if all == 0 {
		return 1
	}
	g := (float64(tail) / 5) / (float64(all) / float64(len(costs)))
	return math.Min(2, math.Max(1, g))
}

// WillOverrun is the pause rule: past 70% of budget AND p50 more than what is left.
func WillOverrun(spent, budget int64, f Forecast) bool {
	if budget <= 0 || !f.HasPlan {
		return false
	}
	return float64(spent) >= 0.7*float64(budget) && f.P50 > budget-spent
}
