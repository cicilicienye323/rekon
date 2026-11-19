// Package money represents amounts as integer minor units.
//
// No float ever touches a value in this package. Floats cannot represent most
// decimal fractions exactly, so a reconciliation built on them reports
// mismatches that do not exist and hides ones that do.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// exponent is the number of fractional digits a currency uses, per ISO 4217.
// Parse refuses a currency that is not listed rather than assuming two digits:
// JPY and VND have none, and guessing scales an amount by a factor of 100.
var exponent = map[string]int{
	"IDR": 2,
	"USD": 2,
	"SGD": 2,
	"MYR": 2,
	"PHP": 2,
	"THB": 2,
	"HKD": 2,
	"JPY": 0,
	"VND": 0,
}

// Amount is a value in the minor units of Currency.
// IDR 1,500.00 is Amount{Units: 150000, Currency: "IDR"}.
type Amount struct {
	Units    int64
	Currency string
}

var ErrUnknownCurrency = errors.New("unknown currency")

// Parse reads a plain decimal string such as "150000.00".
//
// It rejects thousands separators outright. Indonesian locale writes the
// decimal separator as a comma, so a string containing one is ambiguous:
// stripping it would turn 1,50 into 150. Refusing is the only safe reading.
func Parse(s, currency string) (Amount, error) {
	exp, ok := exponent[currency]
	if !ok {
		return Amount{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return Amount{}, errors.New("empty amount")
	}
	if strings.ContainsAny(s, ",_ ") {
		return Amount{}, fmt.Errorf("ambiguous separator in %q: supply a plain decimal", s)
	}

	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")

	whole, frac, hasFrac := strings.Cut(s, ".")
	if !hasFrac {
		frac = ""
	}
	if len(frac) > exp {
		// Dropping a digit here would silently lose or invent money.
		return Amount{}, fmt.Errorf("%q carries %d fractional digits, %s allows %d",
			s, len(frac), currency, exp)
	}
	frac += strings.Repeat("0", exp-len(frac))

	if whole == "" {
		whole = "0"
	}
	units, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return Amount{}, fmt.Errorf("parse %q: %w", s, err)
	}
	if neg {
		units = -units
	}
	return Amount{Units: units, Currency: currency}, nil
}

// ParseOptional reads a column that a provider may legitimately leave blank.
//
// Xendit leaves the fee, VAT and net columns empty until a row settles, so an
// empty string there means the value does not exist yet. That is not the same
// as zero: zero is a fee that was charged and came to nothing, absent is a fee
// nobody has calculated. Collapsing the two makes every unsettled row look like
// a fee discrepancy.
//
// The second return is false when the column was blank.
func ParseOptional(s, currency string) (Amount, bool, error) {
	if strings.TrimSpace(s) == "" {
		return Amount{Currency: currency}, false, nil
	}
	a, err := Parse(s, currency)
	if err != nil {
		return Amount{}, false, err
	}
	return a, true, nil
}

// String renders the amount the way a report file carries it: a plain decimal,
// no currency code, no separators.
func (a Amount) String() string {
	exp := exponent[a.Currency]
	sign := ""
	u := a.Units
	if u < 0 {
		sign, u = "-", -u
	}
	if exp == 0 {
		return sign + strconv.FormatInt(u, 10)
	}
	div := int64(1)
	for i := 0; i < exp; i++ {
		div *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, u/div, exp, u%div)
}

// Sub returns a - b. Currencies must match: subtracting across currencies is a
// programming error, not a runtime condition to recover from.
func (a Amount) Sub(b Amount) Amount {
	if a.Currency != b.Currency {
		panic("money: currency mismatch " + a.Currency + " vs " + b.Currency)
	}
	return Amount{Units: a.Units - b.Units, Currency: a.Currency}
}
