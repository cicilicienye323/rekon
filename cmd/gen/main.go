// Command gen writes a synthetic reconciliation fixture: an internal order
// export, a provider transaction report, and the list of defects it planted.
//
// The planted list is the point. A matcher can only be verified against an
// answer known in advance, and real settlement files cannot be used because
// they carry customer data and sit under a merchant agreement.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/money"
)

// Illustrative pricing. Real rate cards are negotiated per merchant and are
// commercially confidential, so the generator ships round numbers instead.
// The shape is what matters: a percentage with a per-transaction floor, plus
// VAT levied on the fee rather than on the transaction.
const (
	mdrBps = 250  // 2.50% of gross
	vatBps = 1100 // 11% of the fee
)

var minFee = money.Amount{Units: 500000, Currency: "IDR"} // IDR 5,000.00

// Defect classes. Every class except none and tzEdge is a real finding the
// matcher must report. tzEdge is the opposite: a row that looks broken to a
// naive matcher and must still come out matched.
const (
	none        = "matched"
	missingProv = "missing_in_provider"
	missingInt  = "missing_in_internal"
	amountMism  = "amount_mismatch"
	duplicate   = "duplicate"
	feeMism     = "fee_mismatch"
	notSettled  = "not_yet_settled"
	tzEdge      = "timezone_edge"
)

var defectClasses = []string{
	missingProv, missingInt, amountMism, duplicate, feeMism, notSettled,
}

type txn struct {
	orderID   string
	reference string
	gross     money.Amount
	paidAt    time.Time // always UTC internally
	defect    string
}

type fixture struct {
	Seed     int64             `json:"seed"`
	Count    int               `json:"count"`
	Timezone string            `json:"provider_timezone"`
	Defects  map[string]string `json:"defects"`
	Counts   map[string]int    `json:"counts"`
}

func main() {
	var (
		n      = flag.Int("n", 5000, "number of transactions")
		seed   = flag.Int64("seed", 42, "random seed; the same seed always produces the same fixture")
		rate   = flag.Float64("defect-rate", 0.03, "fraction of rows carrying a planted defect")
		tzName = flag.String("tz", "+0700", "timezone the provider report is written in (+0000 for an API export, +0700 for a WIB dashboard export)")
		outDir = flag.String("out", "testdata", "output directory")
	)
	flag.Parse()

	if err := run(*n, *seed, *rate, *tzName, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(n int, seed int64, rate float64, tzName, outDir string) error {
	if n < 1 {
		return fmt.Errorf("-n must be at least 1, got %d", n)
	}
	if rate < 0 || rate > 1 {
		return fmt.Errorf("-defect-rate must be between 0 and 1, got %v", rate)
	}
	offset, err := parseOffset(tzName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	rng := rand.New(rand.NewSource(seed))
	txns := build(rng, n, rate)

	f := fixture{
		Seed:     seed,
		Count:    n,
		Timezone: tzName,
		Defects:  map[string]string{},
		Counts:   map[string]int{},
	}
	for _, t := range txns {
		f.Counts[t.defect]++
		if t.defect != none {
			f.Defects[t.reference] = t.defect
		}
	}

	if err := writeInternal(filepath.Join(outDir, "internal.csv"), txns); err != nil {
		return err
	}
	if err := writeProvider(filepath.Join(outDir, "provider.csv"), txns, tzName, offset); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "expected.json"), f); err != nil {
		return err
	}

	fmt.Printf("wrote %s: %d transactions, provider report in %s\n", outDir, n, tzName)
	for _, k := range sortedKeys(f.Counts) {
		fmt.Printf("  %-20s %d\n", k, f.Counts[k])
	}
	return nil
}

func build(rng *rand.Rand, n int, rate float64) []txn {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := make([]txn, 0, n)

	for i := 1; i <= n; i++ {
		t := txn{
			orderID:   fmt.Sprintf("ORD-%06d", i),
			reference: fmt.Sprintf("REF-%06d", i),
			gross:     randomGross(rng),
			paidAt:    base.Add(time.Duration(rng.Intn(7*24*60)) * time.Minute),
			defect:    none,
		}

		switch {
		case rng.Float64() < rate:
			t.defect = defectClasses[rng.Intn(len(defectClasses))]
		case rng.Float64() < 0.08:
			// Not a defect. Pull the payment into 00:00-07:00 WIB, which in UTC
			// lands on the previous calendar day. A matcher that compares dates
			// without applying the Timezone column marks these missing.
			day := t.paidAt.Truncate(24 * time.Hour)
			t.paidAt = day.Add(time.Duration(rng.Intn(7*60)) * time.Minute).Add(-7 * time.Hour)
			t.defect = tzEdge
		}
		out = append(out, t)
	}
	return out
}

// randomGross skews small so a meaningful share of rows fall under the fee
// floor. That is where a percentage-only fee check goes wrong.
func randomGross(rng *rand.Rand) money.Amount {
	var rupiah int64
	switch rng.Intn(10) {
	case 0, 1, 2, 3:
		rupiah = int64(10_000 + rng.Intn(190_000)) // under or near the floor
	case 4, 5, 6, 7:
		rupiah = int64(200_000 + rng.Intn(2_000_000))
	default:
		rupiah = int64(2_000_000 + rng.Intn(48_000_000))
	}
	return money.Amount{Units: rupiah * 100, Currency: "IDR"}
}

// feeFor applies the percentage, then the floor. The floor is what a flat
// percentage check misses.
func feeFor(gross money.Amount) money.Amount {
	fee := money.Amount{Units: gross.Units * mdrBps / 10000, Currency: gross.Currency}
	if fee.Units < minFee.Units {
		fee = minFee
	}
	return fee
}

func writeInternal(path string, txns []txn) error {
	return writeCSV(path,
		[]string{"order_id", "reference", "gross", "currency", "paid_at", "status"},
		func(w *csv.Writer) error {
			for _, t := range txns {
				if t.defect == missingInt {
					continue // the provider settled something we never recorded
				}
				rec := []string{
					t.orderID, t.reference, t.gross.String(), t.gross.Currency,
					t.paidAt.Format(time.RFC3339), "PAID",
				}
				if err := w.Write(rec); err != nil {
					return err
				}
			}
			return nil
		})
}

// Column names and value shapes below are copied from a real sandbox export,
// not from the documentation. Three of them differ from what the docs describe:
// the withholding column is "Xendit Withholding Tax", the Timezone column can
// carry a zone name rather than an offset, and the money columns are blank
// until a row settles.
func writeProvider(path string, txns []txn, tzName string, offset time.Duration) error {
	header := []string{
		"Status", "Type", "Channel", "Currency", "Amount",
		"Transaction Fee Amount", "Transaction VAT Amount", "Xendit Withholding Tax",
		"Net Amount", "Reference", "Transaction ID",
		"Payment Date", "Timezone", "Settlement Status", "Settlement Date",
		"Estimated Settlement Date",
	}
	loc := time.FixedZone(tzName, int(offset.Seconds()))

	// A dashboard export writes the zone by name, an API export writes an
	// offset. Both reach the matcher, so the fixture carries whichever the
	// caller asked for.
	tzField := tzName
	if offset == 0 {
		tzField = "UTC"
	}

	// The provider report is a ledger of everything that moved money, not only
	// customer payments. None of these has a counterpart in an order export.
	noise := []struct{ typ, channel string }{
		{"TOPUP", "BANK"}, {"DISBURSEMENT", "MANDIRI"}, {"REFUND", "CIMB"},
	}

	return writeCSV(path, header, func(w *csv.Writer) error {
		for i, t := range txns {
			if t.defect == missingProv {
				continue // we think it was paid; the provider has no such row
			}

			gross := t.gross
			if t.defect == amountMism {
				gross = money.Amount{Units: gross.Units + 250000, Currency: gross.Currency}
			}

			settled := t.defect != notSettled
			paid := t.paidAt.In(loc).Format(time.RFC3339)
			estimate := t.paidAt.Add(6 * 24 * time.Hour).In(loc).Format(time.RFC3339)

			// Fee, VAT and net exist only once the money has moved. Leaving them
			// blank is what the provider actually does, and it is the case a
			// matcher is most likely to get wrong.
			feeS, vatS, whtS, netS, settleDate := "", "", "", "", ""
			if settled {
				fee := feeFor(gross)
				vat := money.Amount{Units: fee.Units * vatBps / 10000, Currency: fee.Currency}
				wht := money.Amount{Units: 0, Currency: fee.Currency}
				net := gross.Sub(fee).Sub(vat).Sub(wht)
				if t.defect == feeMism {
					net = money.Amount{Units: net.Units + 100000, Currency: net.Currency}
				}
				feeS, vatS, whtS, netS = fee.String(), vat.String(), wht.String(), net.String()
				settleDate = t.paidAt.Add(26 * time.Hour).In(loc).Format(time.RFC3339)
			}

			settleStatus := "SETTLED"
			if !settled {
				settleStatus = "PENDING"
			}

			rec := []string{
				"SUCCESSFUL", "PAYMENT", channelFor(gross), gross.Currency, gross.String(),
				feeS, vatS, whtS, netS,
				t.reference, fmt.Sprintf("txn_%08d", i+1),
				paid, tzField, settleStatus, settleDate, estimate,
			}
			if err := w.Write(rec); err != nil {
				return err
			}
			if t.defect == duplicate {
				if err := w.Write(rec); err != nil {
					return err
				}
			}
		}

		// Non-payment rows, appended last. The matcher must count and skip these
		// rather than report them as money received without an order.
		for i, n := range noise {
			amt := money.Amount{Units: int64(500_000+i*250_000) * 100, Currency: "IDR"}
			ts := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC).In(loc).Format(time.RFC3339)
			rec := []string{
				"SUCCESSFUL", n.typ, n.channel, amt.Currency, amt.String(),
				"", "", "", amt.String(),
				fmt.Sprintf("%s-%06d", strings.ToLower(n.typ), i+1),
				fmt.Sprintf("txn_9%07d", i+1),
				ts, tzField, "SETTLED", ts, ts,
			}
			if err := w.Write(rec); err != nil {
				return err
			}
		}
		return nil
	})
}

// channelFor picks a plausible channel from the amount. Virtual accounts and
// retail outlets carry small tickets, cards and wallets the larger ones. The
// value is descriptive only: nothing in the matcher reads it.
func channelFor(gross money.Amount) string {
	switch {
	case gross.Units < 20_000_000:
		return "ALFAMART"
	case gross.Units < 200_000_000:
		return "MANDIRI"
	default:
		return "QRIS"
	}
}

func writeCSV(path string, header []string, body func(*csv.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		return err
	}
	if err := body(w); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func parseOffset(s string) (time.Duration, error) {
	t, err := time.Parse("-0700", s)
	if err != nil {
		return 0, fmt.Errorf("-tz %q: want a form like +0700 or +0000", s)
	}
	_, off := t.Zone()
	return time.Duration(off) * time.Second, nil
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
