// Command rekon reconciles a merchant order export against a payment provider
// transaction report.
//
// Two files in, one report out. Which file is which is worked out from their
// headers, so the order they are given in does not matter. With no arguments,
// or when launched by double-clicking, it opens the local page instead.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/ledger"
	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/report"
	"github.com/cicilicienye323/rekon/internal/web"
	"github.com/cicilicienye323/rekon/internal/xendit"
)

func main() {
	var (
		asJSON = flag.Bool("json", false, "write the report as JSON")
		asHTML = flag.String("html", "", "write the report as a standalone HTML page to this path")
		serve  = flag.Bool("serve", false, "open the local page in a browser instead of reading files")
		port   = flag.Int("port", 0, "port for the local page; 0 picks a free one")
	)
	flag.Usage = usage
	// Go's flag package stops at the first non-flag argument. Nobody remembers
	// that, and "rekon a.csv b.csv -json" is the order people actually type, so
	// the flags are lifted to the front before parsing.
	flag.CommandLine.Parse(reorder(os.Args[1:]))

	// Double-clicking the binary passes no arguments. That is a person, not a
	// script, so give them the page rather than a usage message.
	if *serve || flag.NArg() == 0 {
		if err := web.Serve(*port); err != nil {
			fail(err)
		}
		return
	}
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}

	code, err := run(flag.Arg(0), flag.Arg(1), *asJSON, *asHTML)
	if err != nil {
		fail(err)
	}
	os.Exit(code)
}

func run(pathA, pathB string, asJSON bool, htmlPath string) (int, error) {
	orderPath, providerPath, err := identify(pathA, pathB)
	if err != nil {
		return 0, err
	}

	fo, err := os.Open(orderPath)
	if err != nil {
		return 0, err
	}
	defer fo.Close()
	orders, err := ledger.Read(fo)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", orderPath, err)
	}

	fp, err := os.Open(providerPath)
	if err != nil {
		return 0, err
	}
	defer fp.Close()
	txns, meta, err := xendit.Read(fp)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", providerPath, err)
	}

	now := time.Now().UTC()
	rep := match.Run(orders, txns, now)
	rep.Adapter, rep.Timezone, rep.SkippedByType = meta.Adapter, meta.Timezone, meta.SkippedByType

	providerRows := len(txns)
	for _, n := range meta.SkippedByType {
		providerRows += n
	}
	view := report.NewView(rep, len(orders), providerRows, now)

	if htmlPath != "" {
		f, err := os.Create(htmlPath)
		if err != nil {
			return 0, err
		}
		if err := report.HTML(f, view); err != nil {
			f.Close()
			return 0, err
		}
		f.Close()
		fmt.Fprintf(os.Stderr, "wrote %s\n", htmlPath)
	}

	if asJSON {
		if err := report.JSON(os.Stdout, rep, view); err != nil {
			return 0, err
		}
	} else if htmlPath == "" {
		if err := report.Text(os.Stdout, view); err != nil {
			return 0, err
		}
	}

	// A non-zero exit lets a scheduled job notice without parsing anything.
	if rep.AtRisk() > 0 {
		return 1, nil
	}
	return 0, nil
}

// identify decides which path is the order export and which is the provider
// report by looking at the headers. Asking the operator to remember an order
// would be one more thing to get wrong.
func identify(a, b string) (orderPath, providerPath string, err error) {
	ha, err := header(a)
	if err != nil {
		return "", "", err
	}
	hb, err := header(b)
	if err != nil {
		return "", "", err
	}

	switch {
	case xendit.Recognise(ha) && ledger.Recognise(hb):
		return b, a, nil
	case xendit.Recognise(hb) && ledger.Recognise(ha):
		return a, b, nil
	case xendit.Recognise(ha) && xendit.Recognise(hb):
		return "", "", errors.New("both files look like provider reports: one internal order export is needed")
	case ledger.Recognise(ha) && ledger.Recognise(hb):
		return "", "", errors.New("both files look like internal order exports: one provider report is needed")
	}
	return "", "", errors.New(
		"neither file was recognised as a current Xendit transaction report.\n" +
			"The report retired in July 2026 is no longer readable; download a fresh one from the dashboard")
}

// reorder moves flags ahead of file paths. A flag that takes a value keeps its
// value with it, so "-html out.html" survives the move as a pair.
func reorder(args []string) []string {
	takesValue := map[string]bool{"-html": true, "--html": true, "-port": true, "--port": true}
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		if takesValue[a] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

func header(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	h, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read a header row: %w", path, err)
	}
	return h, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `rekon reconciles an order export against a provider transaction report.

  rekon <file> <file>            print the report
  rekon <file> <file> -json      write it as JSON
  rekon <file> <file> -html out.html
  rekon                          open the local page in a browser

The two files may be given in any order.

Exit code is 1 when anything sits in the money-at-risk group.
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rekon:", err)
	os.Exit(2)
}
