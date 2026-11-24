package price

import "testing"

func TestCost(t *testing.T) {
	e, err := Default().Lookup("claude-sonnet-4-5-20250929")
	if err != nil {
		t.Fatal(err)
	}
	// 1000 in*$3 + 500 out*$15 + 2000 cr*$0.3 + 100 cw*$3.75 = 3000+7500+600+375
	if got := e.Cost(Usage{Input: 1000, Output: 500, CacheRead: 2000, CacheWrite: 100}); got != 11475 {
		t.Fatalf("got %d", got)
	}
}

func TestLookupNeverBorrowsAnotherModelsPrice(t *testing.T) {
	tb := Default()
	if e, _ := tb.Lookup("gpt-4o-mini-2024-07-18"); e.Input != 0.15 {
		t.Fatal("dated gpt-4o-mini must price as gpt-4o-mini")
	}
	if e, _ := tb.Lookup("gpt-4o-2024-08-06"); e.Input != 2.5 {
		t.Fatal("dated gpt-4o must price as gpt-4o")
	}
	for _, m := range []string{"claude-opus-4", "claude-opus-5-5-preview", "gpt-5.4-mini-fast", "mystery-1"} {
		if _, err := tb.Lookup(m); err == nil {
			t.Fatalf("%s must be unknown, not priced as a neighbour", m)
		}
	}
}

func TestLongContextTier(t *testing.T) {
	e, _ := Default().Lookup("claude-haiku-5-5")
	short := e.Cost(Usage{Input: 100_000, Output: 1000}) // 100k*0.1 + 1k*0.5
	long := e.Cost(Usage{Input: 100_001, Output: 1000})  // 100001*0.5 + 1k*2.5
	if short != 10_500 || long != 52_501 {
		t.Fatalf("short %d long %d", short, long)
	}
	if e.MaxCost(200_000, 0) != 200_000 { // worst input rate in the long tier: 1-hour cache write, 2 x $0.50
		t.Fatal("max cost must use the long tier for long prompts")
	}
}

func TestMaxCostBounds(t *testing.T) {
	e, _ := Default().Lookup("claude-sonnet-4-5")
	if e.MaxCost(1000, 1000) < e.Cost(Usage{Input: 1000, Output: 1000}) {
		t.Fatal("max below actual")
	}
}

func TestOneHourCacheWrite(t *testing.T) {
	e, _ := Default().Lookup("claude-sonnet-4-5")
	// 1M 5-minute writes at $3.75 + 1M 1-hour writes at 2 x $3 = $9.75
	if got := e.Cost(Usage{CacheWrite: 1_000_000, CacheWrite1h: 1_000_000}); got != 9_750_000 {
		t.Fatalf("got %d", got)
	}
	o, _ := Default().Lookup("gpt-5")
	if o.MaxCost(1000, 0) != 1250 { // OpenAI has no cache writes: worst input rate stays $1.25
		t.Fatalf("openai max %d", o.MaxCost(1000, 0))
	}
}
