// Package match holds the canonical records and the reconciliation itself.
//
// The record types live here rather than in the adapters because they are the
// shape every adapter has to produce. A provider package converts its own
// column names into these and is then finished; nothing downstream of this
// point knows which provider the file came from.
//
// Run performs no file IO. Slices in, Report out, which is what makes it
// testable against a fixture whose answer is known in advance.
package match

import (
	"fmt"
	"sort"
	"time"

	"github.com/cicilicienye323/rekon/internal/money"
)

// Order is one row from the merchant's own export.
type Order struct {
	OrderID   string
	Reference string
	Gross     money.Amount
	PaidAt    time.Time // UTC
	Status    string
	SourceRow int
}

// Txn is one payment row from a provider report, already normalised.
type Txn struct {
	Reference    string
	TxnID        string
	Channel      string
	Gross        money.Amount
	Fee          money.Amount
	VAT          money.Amount
	Withholding  money.Amount
	Net          money.Amount
	HasFees      bool // false while the row is unsettled and the columns are blank
	Status       string
	SettleStatus string
	PaidAt       time.Time  // UTC
	SettledAt    *time.Time // nil until settled
	EstimatedAt  *time.Time // when the provider expects to settle
	SourceRow    int
}

// Settled reports whether the provider has moved the money.
func (t Txn) Settled() bool { return t.SettleStatus == "SETTLED" }

// Mismatch classes, in the precedence order the classifier applies.
const (
	Duplicate         = "duplicate"
	MissingInProvider = "missing_in_provider"
	MissingInInternal = "missing_in_internal"
	AmountMismatch    = "amount_mismatch"
	FeeMismatch       = "fee_mismatch"
	NotYetSettled     = "not_yet_settled"
	Matched           = "matched"
)

// Classes lists every class so a report can print them in a stable order with
// the severe ones first.
var Classes = []string{
	Duplicate, MissingInProvider, MissingInInternal,
	AmountMismatch, FeeMismatch, NotYetSettled, Matched,
}

// Finding is one row that needs a human, or one row that is clean.
type Finding struct {
	Class     string
	Reference string
	Detail    string
	Delta     *money.Amount // nil where the class has no amount to report
	SourceRow int
}

// Report is the whole outcome of one run.
type Report struct {
	Adapter        string
	Timezone       string
	Currency       string
	Counts         map[string]int
	SkippedByType  map[string]int
	InFlight       money.Amount
	OldestInFlight *time.Time
	PastEstimate   int
	Findings       []Finding
}

// AtRisk is the count of findings in the classes that mean money may be gone.
func (r Report) AtRisk() int {
	return r.Counts[MissingInProvider] + r.Counts[MissingInInternal] +
		r.Counts[AmountMismatch] + r.Counts[Duplicate]
}

// Run reconciles orders against provider transactions.
//
// now decides which in-flight rows are past the provider's own estimate. It is
// a parameter rather than a call to time.Now so a test can pin it.
func Run(orders []Order, txns []Txn, now time.Time) Report {
	currency := "IDR"
	if len(txns) > 0 {
		currency = txns[0].Gross.Currency
	} else if len(orders) > 0 {
		currency = orders[0].Gross.Currency
	}

	rep := Report{
		Currency:      currency,
		Counts:        map[string]int{},
		SkippedByType: map[string]int{},
		InFlight:      money.Amount{Currency: currency},
	}

	// Grouped into a slice, not a single value: a reference appearing twice is
	// an expected finding, and overwriting would hide it.
	byRef := make(map[string][]Txn, len(txns))
	for _, t := range txns {
		byRef[t.Reference] = append(byRef[t.Reference], t)
	}
	seen := make(map[string]bool, len(txns))

	for _, o := range orders {
		rows := byRef[o.Reference]
		seen[o.Reference] = true

		switch {
		case len(rows) == 0:
			rep.add(Finding{
				Class: MissingInProvider, Reference: o.Reference,
				Detail:    "marked paid internally, the provider has no such transaction",
				Delta:     &o.Gross,
				SourceRow: o.SourceRow,
			})
		case len(rows) > 1:
			total := money.Amount{Currency: currency}
			for _, r := range rows {
				total = money.Amount{Units: total.Units + r.Gross.Units, Currency: currency}
			}
			rep.add(Finding{
				Class: Duplicate, Reference: o.Reference,
				Detail:    fmt.Sprintf("settled %d times, one order expected", len(rows)),
				Delta:     &total,
				SourceRow: rows[0].SourceRow,
			})
		default:
			rep.classify(o, rows[0], now)
		}
	}

	// Anything the provider settled that no order accounts for.
	for _, t := range txns {
		if seen[t.Reference] {
			continue
		}
		g := t.Gross
		rep.add(Finding{
			Class: MissingInInternal, Reference: t.Reference,
			Detail:    "provider settled this, no internal order found",
			Delta:     &g,
			SourceRow: t.SourceRow,
		})
	}

	sort.Slice(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.Class != b.Class {
			return rank(a.Class) < rank(b.Class)
		}
		return a.Reference < b.Reference
	})
	return rep
}

// classify assigns exactly one class to a matched pair. The order of the checks
// is the precedence: money at risk outranks bookkeeping noise, so a row that is
// both unsettled and short-paid reports as a shortfall rather than a delay.
func (r *Report) classify(o Order, t Txn, now time.Time) {
	if t.Gross.Units != o.Gross.Units {
		d := t.Gross.Sub(o.Gross)
		r.add(Finding{
			Class: AmountMismatch, Reference: o.Reference,
			Detail:    fmt.Sprintf("order says %s, provider says %s", o.Gross, t.Gross),
			Delta:     &d,
			SourceRow: t.SourceRow,
		})
		return
	}

	// The fee columns are blank until the provider settles, so there is nothing
	// to check on an unsettled row. Testing anyway would flag every payment
	// still in flight.
	if t.HasFees {
		want := t.Gross.Sub(t.Fee).Sub(t.VAT).Sub(t.Withholding)
		if want.Units != t.Net.Units {
			d := t.Net.Sub(want)
			r.add(Finding{
				Class: FeeMismatch, Reference: o.Reference,
				Detail:    fmt.Sprintf("net is %s, gross minus fee, VAT and withholding is %s", t.Net, want),
				Delta:     &d,
				SourceRow: t.SourceRow,
			})
			return
		}
	}

	if !t.Settled() {
		g := t.Gross
		r.InFlight = money.Amount{Units: r.InFlight.Units + g.Units, Currency: g.Currency}
		if r.OldestInFlight == nil || t.PaidAt.Before(*r.OldestInFlight) {
			p := t.PaidAt
			r.OldestInFlight = &p
		}
		detail := "paid, settlement pending"
		if t.EstimatedAt != nil && t.EstimatedAt.Before(now) {
			r.PastEstimate++
			detail = "paid, settlement pending, past the provider estimate"
		}
		r.add(Finding{
			Class: NotYetSettled, Reference: o.Reference,
			Detail: detail, Delta: &g, SourceRow: t.SourceRow,
		})
		return
	}

	r.add(Finding{Class: Matched, Reference: o.Reference, SourceRow: t.SourceRow})
}

func (r *Report) add(f Finding) {
	r.Counts[f.Class]++
	// A clean row is counted but not listed: five thousand of them would bury
	// the ninety that need action.
	if f.Class != Matched {
		r.Findings = append(r.Findings, f)
	}
}

func rank(class string) int {
	for i, c := range Classes {
		if c == class {
			return i
		}
	}
	return len(Classes)
}
