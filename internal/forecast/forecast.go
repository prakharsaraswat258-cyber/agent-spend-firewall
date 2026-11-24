// Package forecast estimates the cost to finish from plan progress.
// remaining ~ steps left x average cost per step so far x context-growth factor.
package forecast

import "math"

type Forecast struct {
	HasPlan    bool
	P50, P90   int64  // micro-dollars still to spend
	Confidence string // "low" until 3 steps are done, then "medium"
}

// Compute: costs are per-call costs in order; done/total are plan steps.
func Compute(spent int64, costs []int64, done, total int) Forecast {
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
	f.P90 = 2 * f.P50 // until there is history to calibrate against
	return f
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
