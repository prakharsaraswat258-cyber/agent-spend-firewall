package price

import (
	"strings"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/money"
)

// Rate is a price in micro-dollars per million tokens: $3/MTok is 3_000_000,
// $0.075/MTok is 75_000. It is a struct, not a number, so a bare literal such as
// Input: 3 can't compile and silently mean $0.000003.
type Rate struct{ m int64 }

// ParseRate reads a price in dollars per million tokens ("3", "0.075", "$15").
func ParseRate(s string) (Rate, error) {
	v, err := money.ParseUSD(s)
	return Rate{v}, err
}

// R is ParseRate for constants in code and tests; it panics on a bad literal.
func R(usd string) Rate {
	r, err := ParseRate(usd)
	if err != nil {
		panic(err)
	}
	return r
}

// MicroPerMTok is the rate in micro-dollars per million tokens.
func (r Rate) MicroPerMTok() int64 { return r.m }
func (r Rate) IsZero() bool        { return r.m == 0 }
func (r Rate) String() string      { return money.Decimal(r.m) }

// UnmarshalJSON reads the number's text, so 0.075 is exactly 75_000, never a float.
func (r *Rate) UnmarshalJSON(b []byte) error {
	v, err := money.ParseUSD(strings.Trim(string(b), `"`))
	if err != nil {
		return err
	}
	r.m = v
	return nil
}

func (r Rate) MarshalJSON() ([]byte, error) { return []byte(money.Decimal(r.m)), nil }

// times returns tokens x rate in micro-dollars x 1e6 (divide by 1e6 once per call).
func (r Rate) times(tokens int64) int64 { return tokens * r.m }

func maxRate(rs ...Rate) Rate {
	var out Rate
	for _, r := range rs {
		if r.m > out.m {
			out = r
		}
	}
	return out
}
