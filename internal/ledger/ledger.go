// Package ledger reads the merchant's own order export.
//
// The format is ours, so it is small and fixed. The provider side is where the
// difficulty lives; see internal/xendit.
package ledger

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

var required = []string{"order_id", "reference", "gross", "currency", "paid_at", "status"}

// Recognise reports whether a header looks like an internal order export. The
// CLI and the picker use it to work out which of the two files is which, so the
// order they arrive in does not matter.
func Recognise(header []string) bool {
	idx := index(header)
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			return false
		}
	}
	return true
}

// Read parses an order export. It stops at the first unreadable row rather than
// skipping it: a reconciliation that silently dropped rows produces a clean
// summary that is wrong.
func Read(r io.Reader) ([]match.Order, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := index(header)
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("internal export is missing the %q column", c)
		}
	}

	var out []match.Order
	row := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		row++
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}

		get := func(name string) string {
			i := idx[name]
			if i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}

		currency := get("currency")
		gross, err := money.Parse(get("gross"), currency)
		if err != nil {
			return nil, fmt.Errorf("row %d, gross: %w", row, err)
		}
		paidAt, err := time.Parse(time.RFC3339, get("paid_at"))
		if err != nil {
			return nil, fmt.Errorf("row %d, paid_at: %w", row, err)
		}

		out = append(out, match.Order{
			OrderID:   get("order_id"),
			Reference: get("reference"),
			Gross:     gross,
			PaidAt:    paidAt.UTC(),
			Status:    get("status"),
			SourceRow: row,
		})
	}
	return out, nil
}

func index(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(strings.Trim(h, "\ufeff"))] = i
	}
	return m
}
