package money

import "testing"

func TestParseRoundTrip(t *testing.T) {
	cases := []struct {
		in       string
		currency string
		units    int64
		out      string
	}{
		{"150000.00", "IDR", 15000000, "150000.00"},
		{"150000", "IDR", 15000000, "150000.00"},
		{"150000.5", "IDR", 15000050, "150000.50"},
		{"0.01", "IDR", 1, "0.01"},
		{"0", "IDR", 0, "0.00"},
		{"-2500.00", "IDR", -250000, "-2500.00"},
		{".5", "USD", 50, "0.50"},
		// VND and JPY carry no fractional digits. A parser that assumes two
		// would scale these by 100.
		{"20000000", "VND", 20000000, "20000000"},
		{"1500", "JPY", 1500, "1500"},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.currency)
		if err != nil {
			t.Fatalf("Parse(%q, %q): %v", c.in, c.currency, err)
		}
		if got.Units != c.units {
			t.Errorf("Parse(%q, %q).Units = %d, want %d", c.in, c.currency, got.Units, c.units)
		}
		if s := got.String(); s != c.out {
			t.Errorf("Parse(%q, %q).String() = %q, want %q", c.in, c.currency, s, c.out)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		in       string
		currency string
		why      string
	}{
		{"150000.00", "XYZ", "unknown currency scales by a guess"},
		{"1,500.00", "IDR", "comma is the Indonesian decimal separator, so it is ambiguous"},
		{"1 500.00", "IDR", "space separator is ambiguous"},
		{"100.005", "IDR", "third digit would be silently dropped"},
		{"100.5", "JPY", "JPY has no fractional digits"},
		{"", "IDR", "empty"},
		{"abc", "IDR", "not a number"},
	}
	for _, c := range cases {
		if _, err := Parse(c.in, c.currency); err == nil {
			t.Errorf("Parse(%q, %q) succeeded, want error: %s", c.in, c.currency, c.why)
		}
	}
}

func TestParseOptional(t *testing.T) {
	// A provider leaves fee, VAT and net blank until a row settles. Blank must
	// come back as absent, never as a zero that a fee check would then compare.
	for _, in := range []string{"", "   "} {
		got, present, err := ParseOptional(in, "IDR")
		if err != nil {
			t.Fatalf("ParseOptional(%q): %v", in, err)
		}
		if present {
			t.Errorf("ParseOptional(%q) reported present, want absent", in)
		}
		if got.Units != 0 || got.Currency != "IDR" {
			t.Errorf("ParseOptional(%q) = %+v, want zero units carrying the currency", in, got)
		}
	}

	got, present, err := ParseOptional("9000", "IDR")
	if err != nil || !present {
		t.Fatalf(`ParseOptional("9000") = %v, %v, %v`, got, present, err)
	}
	if got.Units != 900000 {
		t.Errorf(`ParseOptional("9000").Units = %d, want 900000`, got.Units)
	}

	// A blank is absent, but a malformed value is still an error.
	if _, _, err := ParseOptional("1,50", "IDR"); err == nil {
		t.Error(`ParseOptional("1,50") succeeded, want error on the ambiguous separator`)
	}
}

func TestSub(t *testing.T) {
	gross := Amount{15000000, "IDR"}
	fee := Amount{500000, "IDR"}
	if got := gross.Sub(fee).String(); got != "145000.00" {
		t.Errorf("gross.Sub(fee) = %s, want 145000.00", got)
	}
}
