package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	providerHeader = "Status,Type,Channel,Currency,Amount," +
		"Transaction Fee Amount,Transaction VAT Amount,Xendit Withholding Tax," +
		"Net Amount,Reference,Transaction ID," +
		"Payment Date,Timezone,Settlement Status,Settlement Date,Estimated Settlement Date\n"
	ledgerHeader = "order_id,reference,gross,currency,paid_at,status\n"
	// The format retired on 9 July 2026. No Timezone column, and the withholding
	// column under its old name.
	retiredHeader = "Status,Type,Currency,Amount,Transaction Fee Amount," +
		"Transaction VAT Amount,Withholding Tax Amount,Net Amount,Reference," +
		"Payment Date,Settlement Status\n"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The operator is never asked to remember which file goes first. Getting this
// wrong would swap the two sides of every comparison, and the report would look
// entirely plausible.
func TestIdentifyIsIndifferentToOrder(t *testing.T) {
	prov := write(t, "provider.csv", providerHeader)
	ord := write(t, "orders.csv", ledgerHeader)

	for _, c := range []struct{ a, b string }{{prov, ord}, {ord, prov}} {
		gotOrder, gotProvider, err := identify(c.a, c.b)
		if err != nil {
			t.Fatalf("identify(%s, %s): %v", filepath.Base(c.a), filepath.Base(c.b), err)
		}
		if gotOrder != ord {
			t.Errorf("order file = %s, want %s", filepath.Base(gotOrder), filepath.Base(ord))
		}
		if gotProvider != prov {
			t.Errorf("provider file = %s, want %s", filepath.Base(gotProvider), filepath.Base(prov))
		}
	}
}

func TestIdentifyRefusesAmbiguousPairs(t *testing.T) {
	prov := write(t, "provider.csv", providerHeader)
	ord := write(t, "orders.csv", ledgerHeader)
	retired := write(t, "old.csv", retiredHeader)
	junk := write(t, "junk.csv", "a,b,c\n1,2,3\n")

	cases := []struct {
		name, a, b, wants string
	}{
		{"two provider reports", prov, prov, "both files look like provider reports"},
		{"two order exports", ord, ord, "both files look like internal order exports"},
		{"the retired format", prov, retired, "neither file was recognised"},
		{"something else entirely", ord, junk, "neither file was recognised"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := identify(c.a, c.b)
			if err == nil {
				t.Fatal("the pair was accepted")
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %q, want it to mention %q", err, c.wants)
			}
		})
	}
}

// The retired format is the one a real operator will hit, because it is what an
// export saved before July 2026 looks like. The message has to point at the fix
// rather than at the parser.
func TestRetiredFormatErrorPointsAtTheFix(t *testing.T) {
	prov := write(t, "provider.csv", providerHeader)
	retired := write(t, "old.csv", retiredHeader)

	_, _, err := identify(prov, retired)
	if err == nil {
		t.Fatal("the retired format was accepted")
	}
	for _, want := range []string{"July 2026", "download a fresh one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestIdentifyReportsAMissingFile(t *testing.T) {
	ord := write(t, "orders.csv", ledgerHeader)
	if _, _, err := identify(ord, filepath.Join(t.TempDir(), "nope.csv")); err == nil {
		t.Error("a missing file was accepted")
	}
}

// Go's flag package stops at the first non-flag argument, so "rekon a.csv b.csv
// -json" would silently ignore the flag. Nobody types flags first.
func TestReorderLiftsFlagsAheadOfPaths(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"a.csv", "b.csv", "-json"}, []string{"-json", "a.csv", "b.csv"}},
		{[]string{"-json", "a.csv", "b.csv"}, []string{"-json", "a.csv", "b.csv"}},
		{[]string{"a.csv", "-html", "out.html", "b.csv"}, []string{"-html", "out.html", "a.csv", "b.csv"}},
		{[]string{"a.csv", "b.csv", "-port", "8080"}, []string{"-port", "8080", "a.csv", "b.csv"}},
		{[]string{"a.csv", "b.csv"}, []string{"a.csv", "b.csv"}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := reorder(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("reorder(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The exit code is how a scheduled job notices without parsing anything: 1 when
// money is at risk, 0 when the day is clean.
func TestExitCodeFollowsTheRisk(t *testing.T) {
	dir := t.TempDir()

	clean := filepath.Join(dir, "clean.csv")
	os.WriteFile(clean, []byte(ledgerHeader+
		"ORD-1,REF-1,150000.00,IDR,2026-09-01T10:00:00Z,PAID\n"), 0o600)
	matching := filepath.Join(dir, "matching.csv")
	os.WriteFile(matching, []byte(providerHeader+
		"SUCCESSFUL,PAYMENT,MANDIRI,IDR,150000.00,9000.00,990.00,0.00,140010.00,"+
		"REF-1,txn_1,2026-09-01T10:00:00Z,UTC,SETTLED,2026-09-02T10:00:00Z,2026-09-07T10:00:00Z\n"), 0o600)

	code, err := run(clean, matching, false, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("clean run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d on a clean run, want 0", code)
	}

	// The same order with no counterpart at all.
	empty := filepath.Join(dir, "empty.csv")
	os.WriteFile(empty, []byte(providerHeader), 0o600)

	code, err = run(clean, empty, false, filepath.Join(dir, "out2.html"))
	if err != nil {
		t.Fatalf("risky run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d with money at risk, want 1", code)
	}
}
