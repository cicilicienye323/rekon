package ledger

import (
	"strings"
	"testing"
)

const header = "order_id,reference,gross,currency,paid_at,status\n"

func TestReadParsesAnOrder(t *testing.T) {
	orders, err := Read(strings.NewReader(header +
		"ORD-1,REF-1,150000.00,IDR,2026-09-01T10:00:00Z,PAID\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("orders = %d, want 1", len(orders))
	}
	o := orders[0]
	if o.Gross.Units != 15_000_000 {
		t.Errorf("gross = %d minor units, want 15000000", o.Gross.Units)
	}
	if !o.PaidAt.Equal(o.PaidAt.UTC()) {
		t.Error("paid_at was not normalised to UTC")
	}
	if o.SourceRow != 2 {
		t.Errorf("source row = %d, want 2", o.SourceRow)
	}
}

// A missing column is named, so the person exporting the file knows what to add.
func TestReadNamesAMissingColumn(t *testing.T) {
	_, err := Read(strings.NewReader("order_id,reference,gross,currency,status\n"))
	if err == nil {
		t.Fatal("a header with no paid_at was accepted")
	}
	if !strings.Contains(err.Error(), "paid_at") {
		t.Errorf("error = %q, want it to name paid_at", err)
	}
}

// A bad row stops the run rather than being skipped. A reconciliation that
// silently dropped forty rows produces a clean summary that is wrong.
func TestReadStopsOnABadRowAndSaysWhich(t *testing.T) {
	cases := map[string]string{
		"an unparseable amount":  "ORD-1,REF-1,not-a-number,IDR,2026-09-01T10:00:00Z,PAID\n",
		"an ambiguous separator": "ORD-1,REF-1,\"1,50\",IDR,2026-09-01T10:00:00Z,PAID\n",
		"a broken timestamp":     "ORD-1,REF-1,150000.00,IDR,yesterday,PAID\n",
		"an unknown currency":    "ORD-1,REF-1,150000.00,XYZ,2026-09-01T10:00:00Z,PAID\n",
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Read(strings.NewReader(header + row))
			if err == nil {
				t.Fatal("the row was accepted")
			}
			if !strings.Contains(err.Error(), "row 2") {
				t.Errorf("error = %q, want it to name the row", err)
			}
		})
	}
}

func TestRecognise(t *testing.T) {
	if !Recognise(strings.Split(strings.TrimSpace(header), ",")) {
		t.Error("a valid order header was not recognised")
	}
	if Recognise([]string{"Status", "Type", "Amount"}) {
		t.Error("a provider header was recognised as an order export")
	}
}
