// Package xendit turns a Xendit Transaction Report into canonical records.
//
// Every quirk this package works around was found by opening a real export
// rather than by reading the documentation, which is wrong about three of them:
// the withholding column has a different name, the Timezone column carries a
// zone name rather than an offset, and the money columns are blank until a row
// settles. See xendit-schema.pdf.
//
// Adding a second provider means writing a second file shaped like this one.
// Nothing downstream changes.
package xendit

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

// Name identifies the adapter in the report header.
const Name = "xendit transaction report"

// Columns the matcher cannot work without. The export ships either 29 or 92
// columns depending on what the operator ticked, so everything is found by
// name and the extras are ignored.
var required = []string{
	"Status", "Type", "Currency", "Amount",
	"Transaction Fee Amount", "Transaction VAT Amount", "Xendit Withholding Tax",
	"Net Amount", "Reference", "Payment Date", "Timezone", "Settlement Status",
}

// Meta is what the run learned about the file itself, printed so an operator
// can sanity-check it.
type Meta struct {
	Adapter       string
	Timezone      string
	SkippedByType map[string]int
}

// Recognise reports whether a header belongs to this provider.
func Recognise(header []string) bool {
	idx := index(header)
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			return false
		}
	}
	return true
}

// Read parses a Transaction Report. Only rows of Type PAYMENT are returned.
// Every other type is counted in Meta.SkippedByType so the omission is visible
// rather than silent.
func Read(r io.Reader) ([]match.Txn, Meta, error) {
	meta := Meta{Adapter: Name, SkippedByType: map[string]int{}}

	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, meta, fmt.Errorf("read header: %w", err)
	}
	idx := index(header)
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			// Naming the column is the whole point. A generic parse failure sends
			// the operator to an engineer; this sends them back to the dashboard.
			return nil, meta, fmt.Errorf(
				"this does not look like a current Xendit transaction report: the %q column is missing", c)
		}
	}

	var out []match.Txn
	row := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		row++
		if err != nil {
			return nil, meta, fmt.Errorf("row %d: %w", row, err)
		}

		get := func(name string) string {
			i, ok := idx[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}

		typ := get("Type")
		if typ != "PAYMENT" {
			meta.SkippedByType[typ]++
			continue
		}

		zone := get("Timezone")
		if zone == "" {
			return nil, meta, fmt.Errorf("row %d: the Timezone column is empty, so the times cannot be placed", row)
		}
		if meta.Timezone == "" {
			meta.Timezone = zone
		}
		loc, err := zoneOf(zone)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d: %w", row, err)
		}

		currency := get("Currency")
		gross, err := money.Parse(get("Amount"), currency)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Amount: %w", row, err)
		}

		fee, hasFee, err := money.ParseOptional(get("Transaction Fee Amount"), currency)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Transaction Fee Amount: %w", row, err)
		}
		vat, _, err := money.ParseOptional(get("Transaction VAT Amount"), currency)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Transaction VAT Amount: %w", row, err)
		}
		wht, _, err := money.ParseOptional(get("Xendit Withholding Tax"), currency)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Xendit Withholding Tax: %w", row, err)
		}
		net, hasNet, err := money.ParseOptional(get("Net Amount"), currency)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Net Amount: %w", row, err)
		}

		paidAt, err := timestamp(get("Payment Date"), loc)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Payment Date: %w", row, err)
		}
		settledAt, err := optionalTimestamp(get("Settlement Date"), loc)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Settlement Date: %w", row, err)
		}
		estimatedAt, err := optionalTimestamp(get("Estimated Settlement Date"), loc)
		if err != nil {
			return nil, meta, fmt.Errorf("row %d, Estimated Settlement Date: %w", row, err)
		}

		out = append(out, match.Txn{
			Reference:    get("Reference"),
			TxnID:        get("Transaction ID"),
			Channel:      get("Channel"),
			Gross:        gross,
			Fee:          fee,
			VAT:          vat,
			Withholding:  wht,
			Net:          net,
			HasFees:      hasFee && hasNet,
			Status:       get("Status"),
			SettleStatus: strings.ToUpper(get("Settlement Status")),
			PaidAt:       paidAt,
			SettledAt:    settledAt,
			EstimatedAt:  estimatedAt,
			SourceRow:    row,
		})
	}
	return out, meta, nil
}

// zoneOf accepts what the column actually contains. A dashboard export writes a
// zone name such as UTC; an API export writes an offset such as +0700.
func zoneOf(s string) (*time.Location, error) {
	switch strings.ToUpper(s) {
	case "UTC", "GMT", "Z":
		return time.UTC, nil
	}
	t, err := time.Parse("-0700", strings.Replace(s, ":", "", 1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %q as a timezone: expected a name like UTC or an offset like +0700", s)
	}
	_, off := t.Zone()
	return time.FixedZone(s, off), nil
}

// timestamp parses a value that already carries its own offset, and falls back
// to the file's Timezone column when it does not. Fractional seconds are
// accepted: the real export writes them, the fixture does not.
func timestamp(s string, loc *time.Location) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a timestamp", s)
}

func optionalTimestamp(s string, loc *time.Location) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := timestamp(s, loc)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func index(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		h = strings.TrimSpace(strings.Trim(h, "\ufeff"))
		h = strings.Trim(h, `"`)
		m[h] = i
	}
	return m
}
