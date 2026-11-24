package forecast

import (
	"testing"
	"time"
)

func TestBasic(t *testing.T) {
	// 3 of 8 steps done, $6.10 spent: 5 left x $2.0333 = $10.1667 (flat costs, no growth)
	f := Compute(6_100_000, []int64{3_000_000, 3_100_000}, 3, 8)
	if !f.HasPlan || f.P50 != 10_166_667 || f.P90 != 2*f.P50 || f.Confidence != "medium" {
		t.Fatalf("%+v", f)
	}
}

func TestLowConfidenceAndNoPlan(t *testing.T) {
	if f := Compute(1_000_000, nil, 1, 8); f.Confidence != "low" {
		t.Fatal(f)
	}
	if f := Compute(1, nil, 0, 0); f.HasPlan {
		t.Fatal("no plan must not forecast")
	}
}

func TestGrowthClampedAndApplied(t *testing.T) {
	costs := []int64{10, 10, 10, 10, 10, 10, 1000, 1000, 1000, 1000, 1000}
	if g := growth(costs); g != 2 {
		t.Fatalf("want clamp 2, got %v", g)
	}
	if growth([]int64{5, 5}) != 1 {
		t.Fatal("short history must not scale")
	}
}

func TestPauseRule(t *testing.T) {
	f := Forecast{HasPlan: true, Method: "plan", P50: 5_000_000}
	if !WillOverrun(7_000_000, 10_000_000, f) {
		t.Fatal("70% spent and p50 > left must pause")
	}
	if WillOverrun(6_000_000, 10_000_000, f) {
		t.Fatal("under 70%: stay silent")
	}
	if WillOverrun(8_000_000, 10_000_000, Forecast{HasPlan: true, Method: "plan", P50: 1_000_000}) {
		t.Fatal("forecast fits: stay silent")
	}
}

func TestRangeAndCalibration(t *testing.T) {
	f := Compute(6_100_000, []int64{3_000_000, 3_100_000}, 3, 8)
	if f.P10 != f.P50/2 && f.P10 != (f.P50+1)/2 {
		t.Fatalf("default P10 %d for P50 %d", f.P10, f.P50)
	}
	if b := Calibrate([]float64{1, 1}); b != DefaultBand {
		t.Fatal("too little history must keep the default")
	}
	b := Calibrate([]float64{0.9, 1.0, 1.1, 1.2, 1.3, 0.95, 1.05})
	if b.Lo < 0.85 || b.Lo > 0.95 || b.Hi < 1.2 || b.Hi > 1.3 {
		t.Fatalf("tight history should narrow the band: %+v", b)
	}
	if b := Calibrate([]float64{0.01, 0.01, 0.01, 50, 50, 50}); b.Lo != 0.2 || b.Hi != 5 {
		t.Fatalf("clamps: %+v", b)
	}
}

func TestRateForecastWithoutPlan(t *testing.T) {
	// $2 over 10 active minutes, typical run 20 min: 10 min left at $0.20/min = $2.
	f := Rate(DefaultBand, 2_000_000, 10*time.Minute, 20*time.Minute)
	if f.Method != "rate" || f.Confidence != "low" || f.P50 != 2_000_000 || f.P10 != 1_000_000 || f.P90 != 4_000_000 {
		t.Fatalf("%+v", f)
	}
	// Past the typical length: a quarter more (40 min active -> 10 min left at $0.05/min).
	if f := Rate(DefaultBand, 2_000_000, 40*time.Minute, 20*time.Minute); f.P50 != 500_000 {
		t.Fatalf("%+v", f)
	}
	// Too early to say.
	if f := Rate(DefaultBand, 2_000_000, time.Minute, 20*time.Minute); f.Valid() {
		t.Fatal("under 2 minutes of spending must not forecast")
	}
	if !WillOverrun(8_000_000, 10_000_000, Forecast{Method: "rate", P50: 3_000_000}) {
		t.Fatal("rate forecasts use the same pause rule")
	}
}
