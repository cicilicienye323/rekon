// Package match_test is an external test package so it can import the readers.
// The readers import match, so an internal test file would be a cycle.
package match_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cicilicienye323/rekon/internal/ledger"
	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/xendit"
)

// fixture is the shape cmd/gen writes alongside the two CSV files.
type fixture struct {
	Counts map[string]int `json:"counts"`
}

func load(t *testing.T) ([]match.Order, []match.Txn, xendit.Meta, fixture) {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata")

	fi, err := os.Open(filepath.Join(dir, "internal.csv"))
	if err != nil {
		t.Fatalf("open internal.csv: %v", err)
	}
	defer fi.Close()
	orders, err := ledger.Read(fi)
	if err != nil {
		t.Fatalf("read internal.csv: %v", err)
	}

	fp, err := os.Open(filepath.Join(dir, "provider.csv"))
	if err != nil {
		t.Fatalf("open provider.csv: %v", err)
	}
	defer fp.Close()
	txns, meta, err := xendit.Read(fp)
	if err != nil {
		t.Fatalf("read provider.csv: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatalf("read expected.json: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse expected.json: %v", err)
	}
	return orders, txns, meta, f
}

// TestAgainstOracle is the scenario the whole fixture exists for. The generator
// recorded every defect it planted; the matcher has to find exactly those and
// invent none.
func TestAgainstOracle(t *testing.T) {
	orders, txns, _, f := load(t)
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	rep := match.Run(orders, txns, now)

	// timezone_edge rows are not defects. They look wrong to a naive matcher
	// because their WIB and UTC dates differ, and they must come out matched.
	wantMatched := f.Counts["matched"] + f.Counts["timezone_edge"]

	want := map[string]int{
		match.Matched:           wantMatched,
		match.MissingInProvider: f.Counts["missing_in_provider"],
		match.MissingInInternal: f.Counts["missing_in_internal"],
		match.AmountMismatch:    f.Counts["amount_mismatch"],
		match.Duplicate:         f.Counts["duplicate"],
		match.FeeMismatch:       f.Counts["fee_mismatch"],
		match.NotYetSettled:     f.Counts["not_yet_settled"],
	}
	for class, n := range want {
		if got := rep.Counts[class]; got != n {
			t.Errorf("%s = %d, want %d", class, got, n)
		}
	}

	if len(rep.Counts) != len(want) {
		t.Errorf("matcher produced %d classes, want %d: %v", len(rep.Counts), len(want), rep.Counts)
	}
}

// TestTimezoneEdgeDoesNotBecomeAFinding is scenario 5: a payment made between
// midnight and 07:00 WIB falls on the previous calendar day in UTC. Those rows
// are the largest single source of false positives in a hand-rolled matcher.
func TestTimezoneEdgeDoesNotBecomeAFinding(t *testing.T) {
	orders, txns, _, f := load(t)
	rep := match.Run(orders, txns, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))

	edges := f.Counts["timezone_edge"]
	if edges == 0 {
		t.Fatal("fixture carries no timezone edge rows, so this proves nothing")
	}
	if rep.Counts[match.Matched] < edges {
		t.Errorf("matched = %d, which cannot contain the %d timezone edge rows",
			rep.Counts[match.Matched], edges)
	}
}

// TestFeeCheckSkipsUnsettledRows is scenario 3. The provider leaves the fee,
// VAT and net columns blank until a row settles, so an unsettled row has
// nothing to test. Checking anyway would report a fee discrepancy on every
// payment still in flight.
func TestFeeCheckSkipsUnsettledRows(t *testing.T) {
	orders, txns, _, f := load(t)
	rep := match.Run(orders, txns, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))

	unsettled := 0
	for _, tx := range txns {
		if !tx.Settled() {
			unsettled++
		}
	}
	if unsettled == 0 {
		t.Fatal("fixture carries no unsettled rows, so this proves nothing")
	}
	if rep.Counts[match.FeeMismatch] != f.Counts["fee_mismatch"] {
		t.Errorf("fee_mismatch = %d, want %d: unsettled rows are leaking into the fee check",
			rep.Counts[match.FeeMismatch], f.Counts["fee_mismatch"])
	}
}

// TestInFlightIsSummarised is scenario 7 plus the reporting decision that came
// out of the sandbox export: an in-flight bucket is only useful if it carries
// the amount and the age, not just a row count.
func TestInFlightIsSummarised(t *testing.T) {
	orders, txns, _, _ := load(t)
	rep := match.Run(orders, txns, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))

	if rep.Counts[match.NotYetSettled] == 0 {
		t.Fatal("fixture carries no unsettled rows")
	}
	if rep.InFlight.Units == 0 {
		t.Error("InFlight is zero while rows sit in not_yet_settled")
	}
	if rep.OldestInFlight == nil {
		t.Error("OldestInFlight is nil while rows sit in not_yet_settled")
	}
	// Every fixture estimate is six days after payment, and the run date is well
	// past all of them, so all in-flight rows are overdue.
	if rep.PastEstimate != rep.Counts[match.NotYetSettled] {
		t.Errorf("PastEstimate = %d, want %d", rep.PastEstimate, rep.Counts[match.NotYetSettled])
	}
}

// TestNonPaymentRowsNeverReachTheMatcher is scenario 16. The provider file is a
// ledger of everything that moved money. Disbursements and refunds have no
// counterpart in an order export, so matching them would manufacture findings.
func TestNonPaymentRowsNeverReachTheMatcher(t *testing.T) {
	_, txns, meta, _ := load(t)

	if len(meta.SkippedByType) == 0 {
		t.Fatal("fixture carries no non-payment rows, so this proves nothing")
	}
	for _, tx := range txns {
		if tx.Reference == "" {
			t.Errorf("row %d reached the matcher with no reference", tx.SourceRow)
		}
	}
	for _, want := range []string{"TOPUP", "DISBURSEMENT", "REFUND"} {
		if meta.SkippedByType[want] == 0 {
			t.Errorf("type %s was not counted as skipped: %v", want, meta.SkippedByType)
		}
	}
}
