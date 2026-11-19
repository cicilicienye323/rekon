// An internal test package: thousands and slug are unexported, and they are
// exactly where a silent formatting bug lives. The slug bug that shipped once
// broke nothing in Go and nothing in the JSON. It only dropped the colour from
// one band of the HTML report, which no Go test would ever have noticed.
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

func idr(rupiah int64) money.Amount {
	return money.Amount{Units: rupiah * 100, Currency: "IDR"}
}

var at = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

func sample() match.Report {
	oldest := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	g1, g2, g3 := idr(200_000), idr(50_000), idr(300_000)
	return match.Report{
		Adapter: "xendit transaction report", Timezone: "+0700", Currency: "IDR",
		Counts: map[string]int{
			match.Matched: 10, match.MissingInProvider: 2,
			match.AmountMismatch: 1, match.NotYetSettled: 1,
		},
		SkippedByType:  map[string]int{"TOPUP": 1, "REFUND": 2},
		InFlight:       g3,
		OldestInFlight: &oldest,
		PastEstimate:   1,
		Findings: []match.Finding{
			{Class: match.MissingInProvider, Reference: "REF-1", Delta: &g1, SourceRow: 5},
			{Class: match.MissingInProvider, Reference: "REF-2", Delta: &g2, SourceRow: 6},
			{Class: match.AmountMismatch, Reference: "REF-3", Delta: &g2, SourceRow: 7},
			{Class: match.NotYetSettled, Reference: "REF-4", Delta: &g3, SourceRow: 8},
		},
	}
}

// The bug that shipped. "In flight, nothing wrong" has a comma in it, and a slug
// that only replaces spaces produces in-flight,-nothing-wrong, which matches no
// stylesheet. The band rendered with no colour and nobody noticed until a
// browser test looked for the class.
func TestSlugDropsPunctuation(t *testing.T) {
	cases := map[string]string{
		"In flight, nothing wrong": "in-flight-nothing-wrong",
		"Money at risk":            "money-at-risk",
		"Worth checking":           "worth-checking",
		"  Leading and trailing  ": "leading-and-trailing",
		"Tier 1, unit":             "tier-1-unit",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every group name the report can emit has to survive the round trip, or a band
// loses its colour again.
func TestEveryGroupNameProducesAUsableClass(t *testing.T) {
	for _, g := range Groups {
		s := slug(g.Name)
		if s == "" {
			t.Errorf("group %q produced an empty class", g.Name)
		}
		for _, r := range s {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Errorf("group %q produced class %q, which contains %q", g.Name, s, r)
			}
		}
	}
}

func TestThousands(t *testing.T) {
	cases := map[string]string{
		"0":            "0",
		"999":          "999",
		"1000":         "1,000",
		"1000000":      "1,000,000",
		"889626811.00": "889,626,811.00",
		"-1234.50":     "-1,234.50",
		"12.34":        "12.34",
	}
	for in, want := range cases {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%q) = %q, want %q", in, got, want)
		}
	}
}

// A class with no findings must not print an empty row, and a group with no
// classes must not print an empty band.
func TestViewOmitsEmptyGroupsAndRows(t *testing.T) {
	rep := sample()
	delete(rep.Counts, match.AmountMismatch)
	delete(rep.Counts, match.NotYetSettled)

	v := NewView(rep, 12, 16, at)
	for _, g := range v.Groups {
		if len(g.Rows) == 0 {
			t.Errorf("group %q was kept with no rows", g.Name)
		}
		for _, r := range g.Rows {
			if r.Count == 0 {
				t.Errorf("row %q was kept with a count of zero", r.Class)
			}
		}
	}
	for _, g := range v.Groups {
		if g.Name == "In flight, nothing wrong" {
			t.Error("the in-flight band was kept with nothing in it")
		}
	}
}

// The headline figure is the sum of the classes that mean money may be gone,
// and nothing else. Folding the in-flight total into it would turn a delay into
// a loss on the front page.
func TestAtRiskTotalExcludesInFlight(t *testing.T) {
	v := NewView(sample(), 12, 16, at)

	if v.AtRiskCount != 3 {
		t.Errorf("AtRiskCount = %d, want 3", v.AtRiskCount)
	}
	want := idr(200_000 + 50_000 + 50_000)
	if v.AtRiskTotal.Units != want.Units {
		t.Errorf("AtRiskTotal = %s, want %s", v.AtRiskTotal, want)
	}
	if v.InFlight.Units != idr(300_000).Units {
		t.Errorf("InFlight = %s, want 300,000.00", v.InFlight)
	}
	if v.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", v.Skipped)
	}
}

// A negative delta is money missing just as much as a positive one is. Summing
// them signed would let two findings cancel each other out.
func TestTotalsUseAbsoluteAmounts(t *testing.T) {
	rep := sample()
	neg := money.Amount{Units: -idr(50_000).Units, Currency: "IDR"}
	rep.Findings[1].Delta = &neg

	v := NewView(rep, 12, 16, at)
	want := idr(200_000 + 50_000 + 50_000)
	if v.AtRiskTotal.Units != want.Units {
		t.Errorf("AtRiskTotal = %s, want %s: a negative delta cancelled a positive one",
			v.AtRiskTotal, want)
	}
}

func TestTextCarriesTheThingsAnOperatorLooksFor(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, NewView(sample(), 12, 16, at)); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"+0700",                      // the detected timezone, checkable at a glance
		"MONEY AT RISK",              // severity first
		"missing_in_provider",        // the class name, for the engineer
		"IDR 300,000.00",             // the in-flight amount, grouped
		"oldest waiting",             // its age
		"past the provider estimate", // and which of them are overdue
		"CLEAN",
		"rows need action",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the text report does not mention %q:\n%s", want, out)
		}
	}
}

func TestTextSaysSoWhenNothingIsWrong(t *testing.T) {
	rep := match.Report{
		Adapter: "x", Timezone: "UTC", Currency: "IDR",
		Counts: map[string]int{match.Matched: 40}, SkippedByType: map[string]int{},
	}
	var b bytes.Buffer
	if err := Text(&b, NewView(rep, 40, 40, at)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Nothing at risk") {
		t.Errorf("a clean run does not say so:\n%s", b.String())
	}
}

// The JSON is what a scheduled job reads, so its field names are part of the
// contract and a finding has to carry the row number to look up.
func TestJSONShape(t *testing.T) {
	rep := sample()
	var b bytes.Buffer
	if err := JSON(&b, rep, NewView(rep, 12, 16, at)); err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal(b.Bytes(), &out); err != nil {
		t.Fatalf("the JSON output does not parse: %v", err)
	}
	for _, key := range []string{
		"adapter", "timezone", "generated_at", "internal_orders", "provider_rows",
		"skipped_by_type", "counts", "at_risk_count", "at_risk_amount",
		"in_flight_amount", "in_flight_past_estimate", "findings",
	} {
		if _, ok := out[key]; !ok {
			t.Errorf("the JSON output has no %q field", key)
		}
	}

	findings, _ := out["findings"].([]any)
	if len(findings) != len(rep.Findings) {
		t.Fatalf("findings = %d, want %d", len(findings), len(rep.Findings))
	}
	first, _ := findings[0].(map[string]any)
	if first["source_row"] == nil {
		t.Error("a finding carries no source_row, so nobody can find the line")
	}
}

// The HTML is a standalone file: emailed, saved, opened months later. It must
// not reach for a stylesheet, a script or a font it will not find.
func TestHTMLIsSelfContained(t *testing.T) {
	var b bytes.Buffer
	if err := HTML(&b, NewView(sample(), 12, 16, at)); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "<style>") {
		t.Error("the page carries no inline stylesheet")
	}
	for _, forbidden := range []string{"<link", "<script", "http://", "https://"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the page reaches outside itself: found %q", forbidden)
		}
	}
	for _, want := range []string{"money-at-risk", "in-flight-nothing-wrong", "IDR"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
}
