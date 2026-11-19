// Tier 2. These run against the export from a personal sandbox account rather
// than against the generated fixture.
//
// The distinction matters. The generator and the matcher were written by the
// same author from the same reading of the documentation, so a misreading
// produces a fixture and a matcher that agree with each other and disagree with
// reality. Three claims in the documentation turned out to be wrong, and every
// one of them passed tier 1. These tests are the guard against that happening
// again.
package xendit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/xendit"
)

const realFile = "xendit-sandbox-2026-09-08.csv"

func readReal(t *testing.T) ([]match.Txn, xendit.Meta) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "real", realFile))
	if err != nil {
		t.Fatalf("open the sandbox export: %v", err)
	}
	defer f.Close()

	txns, meta, err := xendit.Read(f)
	if err != nil {
		t.Fatalf("read the sandbox export: %v", err)
	}
	return txns, meta
}

// Scenario 12. The whole point of tier 2: a file the author did not write parses
// without a single error.
func TestRealExportParses(t *testing.T) {
	txns, _ := readReal(t)
	if len(txns) == 0 {
		t.Fatal("no payments were read from the sandbox export")
	}
	for _, tx := range txns {
		if tx.Reference == "" {
			t.Errorf("row %d has no reference", tx.SourceRow)
		}
		if tx.Gross.Currency == "" {
			t.Errorf("row %d has no currency", tx.SourceRow)
		}
	}
}

// Scenario 13. The documentation calls this column "Withholding Tax Amount".
// The real export calls it "Xendit Withholding Tax". An adapter built from the
// documentation alone fails its header check on the first real file.
func TestWithholdingColumnHasTheRealName(t *testing.T) {
	const documented, actual = "Withholding Tax Amount", "Xendit Withholding Tax"

	head := []string{
		"Status", "Type", "Currency", "Amount",
		"Transaction Fee Amount", "Transaction VAT Amount", documented,
		"Net Amount", "Reference", "Payment Date", "Timezone", "Settlement Status",
	}
	if xendit.Recognise(head) {
		t.Errorf("a header using the documented name %q was accepted; the real column is %q",
			documented, actual)
	}

	head[6] = actual
	if !xendit.Recognise(head) {
		t.Errorf("a header using the real name %q was rejected", actual)
	}
}

// Scenario 14. The documentation says Timezone carries an offset such as +0000.
// A dashboard export writes the literal string UTC. Both have to parse.
func TestTimezoneAcceptsANameAndAnOffset(t *testing.T) {
	_, meta := readReal(t)
	if meta.Timezone != "UTC" {
		t.Errorf("Timezone from the real export = %q, want %q", meta.Timezone, "UTC")
	}

	for _, zone := range []string{"UTC", "+0700", "+07:00", "GMT"} {
		csv := header() + "\n" + row(zone, "2026-09-08T13:00:54.046Z")
		if _, _, err := xendit.Read(strings.NewReader(csv)); err != nil {
			t.Errorf("timezone %q was rejected: %v", zone, err)
		}
	}
	csv := header() + "\n" + row("Jakarta/WIB", "2026-09-08T13:00:54.046Z")
	if _, _, err := xendit.Read(strings.NewReader(csv)); err == nil {
		t.Error("an unreadable timezone was accepted; guessing moves rows to the wrong day")
	}
}

// Scenario 15. The real export writes milliseconds. The fixture does not. Both
// have to parse, or the adapter works on invented data only.
func TestTimestampsWithFractionalSeconds(t *testing.T) {
	txns, _ := readReal(t)
	for _, tx := range txns {
		if tx.PaidAt.IsZero() {
			t.Errorf("row %d has a zero payment date", tx.SourceRow)
		}
	}

	for _, stamp := range []string{
		"2026-09-08T13:00:54.046Z",
		"2026-09-08T13:00:54Z",
		"2026-09-08T20:00:54+07:00",
	} {
		csv := header() + "\n" + row("UTC", stamp)
		if _, _, err := xendit.Read(strings.NewReader(csv)); err != nil {
			t.Errorf("timestamp %q was rejected: %v", stamp, err)
		}
	}
}

// Scenario 16. The provider file is a ledger of everything that moved money.
// The sandbox export carries the account's opening balance as a TOPUP row,
// which has no counterpart in any order export.
func TestNonPaymentRowsAreCountedAndSkipped(t *testing.T) {
	txns, meta := readReal(t)

	if meta.SkippedByType["TOPUP"] != 1 {
		t.Errorf("TOPUP rows skipped = %d, want 1: %v", meta.SkippedByType["TOPUP"], meta.SkippedByType)
	}
	if len(txns) != 7 {
		t.Errorf("payments read = %d, want 7", len(txns))
	}
}

// Scenario 17. The four-term identity, checked against money a provider actually
// moved rather than money the generator invented. Two of the seven payments had
// settled when the export was taken; the other five carry no fee columns at all.
func TestFourTermIdentityOnRealSettledRows(t *testing.T) {
	txns, _ := readReal(t)

	settled := 0
	for _, tx := range txns {
		if !tx.HasFees {
			if tx.Settled() {
				t.Errorf("row %d is settled but carries no fee columns", tx.SourceRow)
			}
			continue
		}
		settled++
		want := tx.Gross.Sub(tx.Fee).Sub(tx.VAT).Sub(tx.Withholding)
		if want.Units != tx.Net.Units {
			t.Errorf("row %d: net is %s, gross minus fee, VAT and withholding is %s",
				tx.SourceRow, tx.Net, want)
		}
	}
	if settled == 0 {
		t.Fatal("no settled rows in the sandbox export, so this proves nothing")
	}
}

// Scenario 2, on real data. Unsettled rows leave the money columns blank. Read
// as zero rather than absent, every one of them becomes a fee discrepancy.
func TestUnsettledRowsCarryNoFees(t *testing.T) {
	txns, _ := readReal(t)

	unsettled := 0
	for _, tx := range txns {
		if tx.Settled() {
			continue
		}
		unsettled++
		if tx.HasFees {
			t.Errorf("row %d is unsettled but reports fee columns as present", tx.SourceRow)
		}
	}
	if unsettled == 0 {
		t.Fatal("no unsettled rows in the sandbox export, so this proves nothing")
	}
}

// A header missing the Timezone column stops the run. Scenario 6, on the path a
// real operator hits when they open an export saved before July 2026.
func TestMissingTimezoneColumnIsRefusedByName(t *testing.T) {
	head := strings.Replace(header(), ",Timezone", "", 1)
	_, _, err := xendit.Read(strings.NewReader(head + "\n"))
	if err == nil {
		t.Fatal("a report with no Timezone column was accepted")
	}
	if !strings.Contains(err.Error(), "Timezone") {
		t.Errorf("the error does not name the missing column: %v", err)
	}
}

func header() string {
	return "Status,Type,Channel,Currency,Amount," +
		"Transaction Fee Amount,Transaction VAT Amount,Xendit Withholding Tax," +
		"Net Amount,Reference,Transaction ID," +
		"Payment Date,Timezone,Settlement Status,Settlement Date,Estimated Settlement Date"
}

func row(zone, stamp string) string {
	return "SUCCESSFUL,PAYMENT,MANDIRI,IDR,150000,9000,990,,140010,REF-1,txn_1," +
		stamp + "," + zone + ",SETTLED," + stamp + "," + stamp
}
