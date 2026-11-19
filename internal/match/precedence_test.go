package match_test

import (
	"testing"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

var runAt = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

func idr(rupiah int64) money.Amount {
	return money.Amount{Units: rupiah * 100, Currency: "IDR"}
}

func order(ref string, gross int64) match.Order {
	return match.Order{
		OrderID: "ORD-" + ref, Reference: ref, Gross: idr(gross),
		PaidAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), Status: "PAID", SourceRow: 2,
	}
}

// settled builds a provider row whose four money columns are consistent.
func settled(ref string, gross, fee int64) match.Txn {
	f, v := idr(fee), money.Amount{Units: idr(fee).Units * 1100 / 10000, Currency: "IDR"}
	w := money.Amount{Currency: "IDR"}
	g := idr(gross)
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	return match.Txn{
		Reference: ref, Gross: g, Fee: f, VAT: v, Withholding: w,
		Net: g.Sub(f).Sub(v).Sub(w), HasFees: true,
		Status: "SUCCESSFUL", SettleStatus: "SETTLED",
		PaidAt: at, SettledAt: &at, SourceRow: 2,
	}
}

// pending builds a row the provider has not settled, so it carries no money
// columns at all. That is what a real export looks like.
func pending(ref string, gross int64) match.Txn {
	est := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	return match.Txn{
		Reference: ref, Gross: idr(gross), HasFees: false,
		Status: "SUCCESSFUL", SettleStatus: "PENDING",
		PaidAt:      time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		EstimatedAt: &est, SourceRow: 2,
	}
}

func classOf(t *testing.T, orders []match.Order, txns []match.Txn) string {
	t.Helper()
	rep := match.Run(orders, txns, runAt)
	for _, c := range match.Classes {
		if rep.Counts[c] > 0 {
			return c
		}
	}
	return "none"
}

// Scenario 4. Virtual accounts charge a flat fee and cards charge a percentage.
// A real sandbox export showed two payments of 1,428,000 and 150,000 both paying
// a flat 9,000. The matcher checks the four-term identity, never the rate, so
// both shapes have to come out clean.
func TestBothFeeShapesSatisfyTheIdentity(t *testing.T) {
	cases := []struct {
		name       string
		gross, fee int64
	}{
		{"flat fee on a small ticket", 150_000, 9_000},
		{"the same flat fee on a large ticket", 1_428_000, 9_000},
		{"a percentage fee", 2_000_000, 50_000},
		{"a percentage fee that lands on a fraction", 1_934_987, 48_374},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classOf(t,
				[]match.Order{order("REF-1", c.gross)},
				[]match.Txn{settled("REF-1", c.gross, c.fee)})
			if got != match.Matched {
				t.Errorf("class = %s, want %s: the identity holds whatever shape the fee took",
					got, match.Matched)
			}
		})
	}
}

// Scenario 9. A row can qualify for several classes at once. Precedence resolves
// it to one, and money at risk has to outrank bookkeeping noise. A payment that
// is both unsettled and short-paid is a shortfall, not a delay: naming it a
// delay tells the operator to wait, and nobody ever looks at it again.
func TestShortfallOutranksDelay(t *testing.T) {
	o := order("REF-1", 500_000)
	tx := pending("REF-1", 400_000) // unsettled and 100,000 short

	got := classOf(t, []match.Order{o}, []match.Txn{tx})
	if got != match.AmountMismatch {
		t.Fatalf("class = %s, want %s", got, match.AmountMismatch)
	}

	// The same row without the shortfall is a delay, which confirms the test
	// above is measuring precedence rather than a broken settlement check.
	got = classOf(t, []match.Order{o}, []match.Txn{pending("REF-1", 500_000)})
	if got != match.NotYetSettled {
		t.Errorf("class = %s, want %s once the amounts agree", got, match.NotYetSettled)
	}
}

// Scenario 10. Duplicates are structural and are resolved before any comparison
// of amounts. While a reference appears twice there is no single row to compare
// against, so reporting an amount mismatch would name the wrong problem and hide
// the clawback that is coming.
func TestDuplicateOutranksAmountMismatch(t *testing.T) {
	o := order("REF-1", 500_000)
	rows := []match.Txn{
		settled("REF-1", 500_000, 9_000),
		settled("REF-1", 480_000, 9_000), // second settlement, and a different amount
	}

	rep := match.Run([]match.Order{o}, rows, runAt)
	if rep.Counts[match.Duplicate] != 1 {
		t.Errorf("duplicate = %d, want 1", rep.Counts[match.Duplicate])
	}
	if rep.Counts[match.AmountMismatch] != 0 {
		t.Errorf("amount_mismatch = %d, want 0: the amounts cannot be compared while the reference is ambiguous",
			rep.Counts[match.AmountMismatch])
	}

	// The finding carries both settlements, because the clawback will be for the
	// total and the operator needs the figure.
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(rep.Findings))
	}
	f := rep.Findings[0]
	if f.Delta == nil || f.Delta.Units != idr(980_000).Units {
		t.Errorf("delta = %v, want the sum of both settlements, 980,000.00", f.Delta)
	}
}

// A settled row whose net breaks the identity is a fee finding, and a row that
// is clean in every respect is matched. Together with the two tests above this
// pins every branch of the classifier.
func TestFeeMismatchAndCleanRow(t *testing.T) {
	o := order("REF-1", 500_000)

	tx := settled("REF-1", 500_000, 9_000)
	tx.Net = money.Amount{Units: tx.Net.Units + 100_00, Currency: "IDR"}
	if got := classOf(t, []match.Order{o}, []match.Txn{tx}); got != match.FeeMismatch {
		t.Errorf("class = %s, want %s", got, match.FeeMismatch)
	}

	if got := classOf(t, []match.Order{o}, []match.Txn{settled("REF-1", 500_000, 9_000)}); got != match.Matched {
		t.Errorf("class = %s, want %s", got, match.Matched)
	}
}
