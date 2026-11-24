package money

import (
	"encoding/json"
	"testing"
)

func TestParseUSD(t *testing.T) {
	for in, want := range map[string]int64{
		"5": 5_000_000, "$5": 5_000_000, "5.25": 5_250_000, "0.000001": 1, ".5": 500_000,
		"0.075": 75_000, " $ 12.3 ": 12_300_000, "0": 0, "1.10": 1_100_000,
	} {
		if got, err := ParseUSD(in); err != nil || got != want {
			t.Errorf("ParseUSD(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "1e3", "abc", "1.2345678", "5,00", "1.2.3", "$"} {
		if _, err := ParseUSD(bad); err == nil {
			t.Errorf("ParseUSD(%q) accepted", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	for m, want := range map[int64]string{1_234_567: "$1.23", 1_235_000: "$1.24", 4_999: "$0.00", 5_000: "$0.01", 0: "$0.00", -2_500_000: "-$2.50"} {
		if got := Format(m); got != want {
			t.Errorf("Format(%d) = %s, want %s", m, got, want)
		}
	}
	if Exact(480_100) != "$0.4801" || Exact(5_000_000) != "$5" || Decimal(75_000) != "0.075" {
		t.Fatal(Exact(480_100), Exact(5_000_000), Decimal(75_000))
	}
}

func TestAmountJSON(t *testing.T) {
	var c struct {
		B Amount `json:"b"`
	}
	for in, want := range map[string]int64{`{"b":5}`: 5_000_000, `{"b":2.5}`: 2_500_000, `{"b":"$0.10"}`: 100_000} {
		if err := json.Unmarshal([]byte(in), &c); err != nil || int64(c.B) != want {
			t.Errorf("%s -> %d %v", in, c.B, err)
		}
	}
	c.B = 100_000
	if b, _ := json.Marshal(c); string(b) != `{"b":0.1}` {
		t.Fatal(string(b))
	}
	if json.Unmarshal([]byte(`{"b":-1}`), &c) == nil {
		t.Fatal("negative accepted")
	}
}
