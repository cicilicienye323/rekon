// Package report renders a match.Report for the three audiences that need it:
// a terminal for the engineer, JSON for a scheduled job, and a page for finance.
//
// None of the renderers decide anything. Grouping and severity come from the
// classes match already assigned; this package only chooses words and layout.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

// Label carries the plain-language wording finance reads. The class names are
// for engineers and for the JSON; nobody outside this repository should have to
// learn what missing_in_provider means.
type Label struct {
	Class string
	Title string
	Note  string
}

// Group is a band of severity. The order here is the order everything prints.
type Group struct {
	Name   string
	Labels []Label
}

var Groups = []Group{
	{"Money at risk", []Label{
		{match.MissingInProvider, "Paid, never settled", "Marked paid internally. The provider has no such transaction."},
		{match.MissingInInternal, "Settled, no order", "Money received that cannot be attributed to an order."},
		{match.Duplicate, "Settled twice", "Same reference paid out more than once. Expect a clawback."},
		{match.AmountMismatch, "Amount disagrees", "Gross on the provider row differs from the order."},
	}},
	{"Worth checking", []Label{
		{match.FeeMismatch, "Fee does not add up", "Net is not gross minus fee, VAT and withholding tax."},
	}},
	{"In flight, nothing wrong", []Label{
		{match.NotYetSettled, "Paid, settlement pending", "The customer paid. The money has not landed yet."},
	}},
}

// View is the flattened, presentable form of a report. Both the text and the
// HTML renderer read this, so the two can never drift apart.
type View struct {
	Adapter       string
	Timezone      string
	Orders        int
	ProviderRows  int
	Skipped       int
	SkippedBy     map[string]int
	AtRiskCount   int
	AtRiskTotal   money.Amount
	Clean         int
	InFlight      money.Amount
	InFlightCount int
	PastEstimate  int
	Oldest        *time.Time
	Now           time.Time
	Groups        []GroupView
}

type GroupView struct {
	Name  string
	Rows  []RowView
	Count int
	Total money.Amount
}

type RowView struct {
	Class string
	Title string
	Note  string
	Count int
	Total money.Amount
}

// NewView folds the report into what the renderers print.
func NewView(rep match.Report, orders, providerRows int, now time.Time) View {
	totals := map[string]money.Amount{}
	for _, f := range rep.Findings {
		if f.Delta == nil {
			continue
		}
		cur := totals[f.Class]
		u := f.Delta.Units
		if u < 0 {
			u = -u
		}
		totals[f.Class] = money.Amount{Units: cur.Units + u, Currency: f.Delta.Currency}
	}

	v := View{
		Adapter: rep.Adapter, Timezone: rep.Timezone,
		Orders: orders, ProviderRows: providerRows,
		SkippedBy: rep.SkippedByType,
		Clean:     rep.Counts[match.Matched],
		InFlight:  rep.InFlight, InFlightCount: rep.Counts[match.NotYetSettled],
		PastEstimate: rep.PastEstimate, Oldest: rep.OldestInFlight, Now: now,
		AtRiskCount: rep.AtRisk(),
		AtRiskTotal: money.Amount{Currency: rep.Currency},
	}
	for _, n := range rep.SkippedByType {
		v.Skipped += n
	}

	for _, g := range Groups {
		gv := GroupView{Name: g.Name, Total: money.Amount{Currency: rep.Currency}}
		for _, l := range g.Labels {
			n := rep.Counts[l.Class]
			if n == 0 {
				continue
			}
			t := totals[l.Class]
			t.Currency = rep.Currency
			gv.Rows = append(gv.Rows, RowView{l.Class, l.Title, l.Note, n, t})
			gv.Count += n
			gv.Total = money.Amount{Units: gv.Total.Units + t.Units, Currency: rep.Currency}
		}
		if len(gv.Rows) > 0 {
			v.Groups = append(v.Groups, gv)
		}
		if g.Name == "Money at risk" {
			v.AtRiskTotal = gv.Total
		}
	}
	return v
}

// Text writes the terminal form. Severity first, the clean count last, and the
// detected timezone in the header so an operator can spot a file that arrived
// in a different zone from yesterday.
func Text(w io.Writer, v View) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "\n  adapter    %s\n", v.Adapter)
	fmt.Fprintf(b, "  timezone   %s\n", v.Timezone)
	fmt.Fprintf(b, "  rows       %s internal   %s provider   %s skipped\n",
		group(v.Orders), group(v.ProviderRows), group(v.Skipped))

	for _, g := range v.Groups {
		fmt.Fprintf(b, "\n\n  %-48s %10s\n", strings.ToUpper(g.Name), group(g.Count))
		for _, r := range g.Rows {
			fmt.Fprintf(b, "    %-22s %5s   %s\n", r.Class, group(r.Count), r.Note)
		}
		if g.Total.Units > 0 {
			fmt.Fprintf(b, "    %-22s %s %s\n", "", g.Total.Currency, thousands(g.Total.String()))
		}
	}

	if v.InFlightCount > 0 && v.Oldest != nil {
		age := int(v.Now.Sub(*v.Oldest).Hours() / 24)
		fmt.Fprintf(b, "\n    oldest waiting %d days, since %s", age, v.Oldest.Format("2 Jan"))
		if v.PastEstimate > 0 {
			fmt.Fprintf(b, "\n    %d past the provider estimate", v.PastEstimate)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(b, "\n\n  %-48s %10s\n", "CLEAN", group(v.Clean))
	if v.AtRiskCount > 0 {
		fmt.Fprintf(b, "\n  %s rows need action, %s %s at risk.\n",
			group(v.AtRiskCount), v.AtRiskTotal.Currency, thousands(v.AtRiskTotal.String()))
	} else {
		b.WriteString("\n  Nothing at risk.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// jsonReport is the machine-readable shape. It is a separate struct so the
// field names stay stable even if the internal types move.
type jsonReport struct {
	Adapter       string         `json:"adapter"`
	Timezone      string         `json:"timezone"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Orders        int            `json:"internal_orders"`
	ProviderRows  int            `json:"provider_rows"`
	SkippedByType map[string]int `json:"skipped_by_type"`
	Counts        map[string]int `json:"counts"`
	AtRiskCount   int            `json:"at_risk_count"`
	AtRiskAmount  string         `json:"at_risk_amount"`
	InFlight      string         `json:"in_flight_amount"`
	PastEstimate  int            `json:"in_flight_past_estimate"`
	Oldest        *time.Time     `json:"in_flight_oldest,omitempty"`
	Findings      []jsonFinding  `json:"findings"`
}

type jsonFinding struct {
	Class     string `json:"class"`
	Reference string `json:"reference"`
	Detail    string `json:"detail"`
	Amount    string `json:"amount,omitempty"`
	SourceRow int    `json:"source_row"`
}

// JSON writes the form a scheduled job consumes.
func JSON(w io.Writer, rep match.Report, v View) error {
	out := jsonReport{
		Adapter: v.Adapter, Timezone: v.Timezone, GeneratedAt: v.Now,
		Orders: v.Orders, ProviderRows: v.ProviderRows,
		SkippedByType: rep.SkippedByType, Counts: rep.Counts,
		AtRiskCount: v.AtRiskCount, AtRiskAmount: v.AtRiskTotal.String(),
		InFlight: v.InFlight.String(), PastEstimate: v.PastEstimate, Oldest: v.Oldest,
	}
	for _, f := range rep.Findings {
		jf := jsonFinding{f.Class, f.Reference, f.Detail, "", f.SourceRow}
		if f.Delta != nil {
			jf.Amount = f.Delta.String()
		}
		out.Findings = append(out.Findings, jf)
	}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return out.Findings[i].Reference < out.Findings[j].Reference
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// group inserts thousands separators into a count.
func group(n int) string { return thousands(fmt.Sprintf("%d", n)) }

// thousands inserts separators into the whole part of a decimal string.
func thousands(s string) string {
	whole, frac, hasFrac := strings.Cut(s, ".")
	neg := strings.HasPrefix(whole, "-")
	whole = strings.TrimPrefix(whole, "-")

	var out []byte
	for i, c := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	res := string(out)
	if neg {
		res = "-" + res
	}
	if hasFrac {
		res += "." + frac
	}
	return res
}
