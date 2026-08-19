// Package money represents INR amounts as integer paise. Never use float64
// for money: floats cannot represent paise exactly and accumulate rounding
// error across fare math (distance * rate * mode multiplier * class
// multiplier * jitter). int64 paise is exact for every operation we need.
package money

import "fmt"

// Paise is an amount of Indian Rupees expressed in paise (1 INR = 100 paise).
type Paise int64

// FormatINR renders paise as a rupee string with two decimal places, e.g.
// Paise(123456).FormatINR() == "₹1234.56". Formatting only ever happens at
// the edge (API responses, logs) - never before arithmetic.
func (p Paise) FormatINR() string {
	rupees := int64(p) / 100
	remainder := int64(p) % 100
	if remainder < 0 {
		remainder = -remainder
	}
	return fmt.Sprintf("₹%d.%02d", rupees, remainder)
}
