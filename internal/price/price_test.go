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

// Pinned from the official pricing pages (2026-11-24). A failing row means the table
// was edited: re-check the page before changing the test.
func TestPinnedPrices(t *testing.T) {
	tb := Default()
	for _, c := range []struct {
		model             string
		in, out, cr, cw1h float64
	}{
		{"claude-opus-5-5", 4, 20, 0.2, 8},
		{"claude-sonnet-5-5", 2, 10, 0.1, 4},
		{"claude-fable-5-2", 8, 40, 0.2, 16},
		{"claude-mythos-5-2", 8, 40, 0.2, 16},
		{"claude-haiku-5-5", 0.1, 0.5, 0.01, 0.2},
		{"claude-haiku-4-5", 1, 5, 0.1, 2},
		{"gpt-6.2-astra", 8, 40, 0.4, 0},
		{"gpt-5.4-mini", 0.75, 4.5, 0.075, 0},
	} {
		e, err := tb.Lookup(c.model)
		if err != nil {
			t.Fatal(err)
		}
		if e.Input != c.in || e.Output != c.out || e.CacheRead != c.cr || e.write1hRate() != c.cw1h {
			t.Errorf("%s: %+v (1h %v)", c.model, e, e.write1hRate())
		}
	}
}

func TestOpenAILongContextAndProCache(t *testing.T) {
	e, _ := Default().Lookup("gpt-6.2-sol")
	short := e.Cost(Usage{Input: 272_000, Output: 1000}) // 272000*2 + 1000*10
	long := e.Cost(Usage{Input: 272_001, Output: 1000})  // 272001*4 + 1000*15
	if short != 554_000 || long != 1_103_004 {
		t.Fatalf("short %d long %d", short, long)
	}
	pro, _ := Default().Lookup("gpt-5.5-pro") // no cached price: cached tokens cost full input
	if c := pro.Cost(Usage{CacheRead: 1000}); c != 30_000 {
		t.Fatalf("pro cached tokens billed %d, want 30000", c)
	}
}
