// Package money parses and formats amounts as integer micro-dollars
// (1 USD = 1,000,000). No floating point touches money: decimals are parsed
// digit by digit, and formatting rounds with integer math.
package money

import (
	"errors"
	"fmt"
	"strings"
)

// Micro is one millionth of a US dollar.
const Micro = 1_000_000

// ParseUSD reads "5", "$5", "5.25" or "0.000001" exactly. Up to 6 decimals;
// more, a sign, an exponent or anything else is an error.
func ParseUSD(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" {
		return 0, errors.New("empty amount")
	}
	if len(frac) > 6 {
		return 0, fmt.Errorf("%q has more than 6 decimals", s)
	}
	var w int64
	for _, c := range whole {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not an amount in dollars", s)
		}
		if w > (1<<62)/10/Micro {
			return 0, fmt.Errorf("%q is too large", s)
		}
		w = w*10 + int64(c-'0')
	}
	var f int64
	for i := 0; i < 6; i++ {
		f *= 10
		if i < len(frac) {
			c := frac[i]
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("%q is not an amount in dollars", s)
			}
			f += int64(c - '0')
		}
	}
	return w*Micro + f, nil
}

// Format shows cents, rounding half away from zero: 1_234_567 -> "$1.23".
func Format(m int64) string {
	neg := m < 0
	if neg {
		m = -m
	}
	cents := (m + 5_000) / 10_000
	s := fmt.Sprintf("$%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// Exact shows every digit that matters: 480_100 -> "$0.4801", 5_000_000 -> "$5".
func Exact(m int64) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	return sign + "$" + Decimal(m)
}

// Decimal writes micro-units as a plain decimal without trailing zeros: 75_000 -> "0.075".
func Decimal(m int64) string {
	s := fmt.Sprintf("%d.%06d", m/Micro, m%Micro)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Flag is a flag.Value for an amount in dollars, stored as micro-dollars.
type Flag struct{ V int64 }

func (f *Flag) String() string { return Decimal(f.V) }

func (f *Flag) Set(s string) error {
	v, err := ParseUSD(s)
	if err != nil {
		return err
	}
	f.V = v
	return nil
}

// Amount is a JSON field in dollars (a number like 5 or 2.50, or a string "$5"),
// kept as micro-dollars. The number is read as text, so it is never a float.
type Amount int64

func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := ParseUSD(s)
	if err != nil {
		return err
	}
	*a = Amount(v)
	return nil
}

func (a Amount) MarshalJSON() ([]byte, error) { return []byte(Decimal(int64(a))), nil }
