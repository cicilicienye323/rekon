package match_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/money"
)

// BenchmarkRun measures the matcher alone, with parsing excluded, on the volume
// the README quotes. Building the input inside the timed loop would measure the
// benchmark rather than the tool.
func BenchmarkRun(b *testing.B) {
	const n = 50000
	orders, txns := synth(n)
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rep := match.Run(orders, txns, now)
		if rep.Counts[match.Matched] == 0 {
			b.Fatal("nothing matched, the benchmark is measuring the wrong thing")
		}
	}
}

// synth builds a pair of slices shaped like a real day: mostly clean, with the
// same spread of defects the fixture plants.
func synth(n int) ([]match.Order, []match.Txn) {
	orders := make([]match.Order, 0, n)
	txns := make([]match.Txn, 0, n)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= n; i++ {
		ref := fmt.Sprintf("REF-%07d", i)
		gross := money.Amount{Units: int64(100_000+i%4_000_000) * 100, Currency: "IDR"}
		paid := base.Add(time.Duration(i%(7*24*60)) * time.Minute)

		orders = append(orders, match.Order{
			OrderID: fmt.Sprintf("ORD-%07d", i), Reference: ref,
			Gross: gross, PaidAt: paid, Status: "PAID", SourceRow: i + 1,
		})

		switch i % 100 {
		case 3: // missing on the provider side
			continue
		case 7: // unsettled, so the fee columns are absent
			txns = append(txns, match.Txn{
				Reference: ref, Gross: gross, Status: "SUCCESSFUL",
				SettleStatus: "PENDING", PaidAt: paid, SourceRow: i + 1,
			})
			continue
		}

		fee := money.Amount{Units: gross.Units * 250 / 10000, Currency: "IDR"}
		vat := money.Amount{Units: fee.Units * 1100 / 10000, Currency: "IDR"}
		wht := money.Amount{Currency: "IDR"}
		txns = append(txns, match.Txn{
			Reference: ref, Gross: gross, Fee: fee, VAT: vat, Withholding: wht,
			Net: gross.Sub(fee).Sub(vat), HasFees: true,
			Status: "SUCCESSFUL", SettleStatus: "SETTLED",
			PaidAt: paid, SourceRow: i + 1,
		})
	}
	return orders, txns
}
