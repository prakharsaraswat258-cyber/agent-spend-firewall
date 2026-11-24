package forecast

import "testing"

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
	f := Forecast{HasPlan: true, P50: 5_000_000}
	if !WillOverrun(7_000_000, 10_000_000, f) {
		t.Fatal("70% spent and p50 > left must pause")
	}
	if WillOverrun(6_000_000, 10_000_000, f) {
		t.Fatal("under 70%: stay silent")
	}
	if WillOverrun(8_000_000, 10_000_000, Forecast{HasPlan: true, P50: 1_000_000}) {
		t.Fatal("forecast fits: stay silent")
	}
}
