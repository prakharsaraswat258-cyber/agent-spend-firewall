package price

import "testing"

func TestCost(t *testing.T) {
	tb := Default()
	e, err := tb.Lookup("claude-sonnet-4-20250514")
	if err != nil {
		t.Fatal(err)
	}
	// 1000 in*$3 + 500 out*$15 + 2000 cr*$0.3 + 100 cw*$3.75 = 3000+7500+600+375
	if got := e.Cost(Usage{1000, 500, 2000, 100}); got != 11475 {
		t.Fatalf("got %d", got)
	}
}

func TestLongestPrefixAndUnknown(t *testing.T) {
	tb := Default()
	e, _ := tb.Lookup("gpt-4o-mini-2024-07-18")
	if e.Input != 0.15 {
		t.Fatal("mini must win over gpt-4o")
	}
	if _, err := tb.Lookup("mystery-1"); err == nil {
		t.Fatal("want error")
	}
}

func TestMaxCostBounds(t *testing.T) {
	e, _ := Default().Lookup("claude-sonnet-4")
	if e.MaxCost(1000, 1000) < e.Cost(Usage{Input: 1000, Output: 1000}) {
		t.Fatal("max below actual")
	}
}
