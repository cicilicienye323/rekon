# Development checklist

Working notes, not documentation. Delete this file when v0.1 ships.

Each task is sized to finish in one sitting. A task is done when its check
passes, not when the code is written.

Reference: `rekon-prd.pdf` for the design, `xendit-schema.pdf` for the columns
and the five traps.

---

## Step 1 — money and fixture generator (done)

- [x] `internal/money`: integer minor units, ISO 4217 exponent table
- [x] `money.Parse` rejects commas, unknown currencies, over-long fractions
- [x] `cmd/gen`: synthetic fixture with six planted defect classes
- [x] `expected.json` written as the test oracle
- [x] Deterministic: same seed, identical output

### Carried over from the sandbox findings

- [x] `money.ParseOptional`: empty string means absent, not zero. Real rows leave
      fee, VAT and net empty until settlement, and `Parse("")` currently errors
- [x] `cmd/gen`: emit some rows with `Type` of REFUND, DISBURSEMENT and TOPUP so
      the type filter has something to skip
- [x] `cmd/gen`: emit unsettled rows with empty fee, VAT and net, matching real
      provider behaviour
- [x] `cmd/gen`: write `Timezone` as a zone name in one mode and an offset in
      another, so both parse paths are exercised
- [x] `cmd/gen`: populate `Estimated Settlement Date` on unsettled rows

---

## Step 2 — readers

Split in two so the parsing layer can be reviewed before anything is built on it.

### 2a: the two readers

- [x] `internal/ledger/ledger.go`: read `internal.csv` into `[]Order`
- [x] `internal/xendit/adapter.go`: validate the header, naming the missing
      column on failure rather than reporting a generic parse error
- [x] Column names copied from a real export, `Xendit Withholding Tax` included
- [x] `Timezone` accepts a zone name (`UTC`) and a numeric offset (`+0700`)
- [x] Timestamps parse with fractional seconds and a `Z` suffix
- [x] Money columns read as absent when empty
- [x] Rows whose `Type` is not `PAYMENT` are counted and dropped
- [x] Both readers run clean over `testdata/real/`

Check: `go test ./internal/...` green, and a scratch main prints 7 payments and
1 skipped topup from the real file.

### 2b: the matcher

- [x] `internal/match`: index provider rows by `Reference` into a slice
- [x] Walk internal orders, then walk unvisited provider rows
- [x] Seven classes assigned by the precedence in the PRD
- [x] `fee_mismatch` evaluated on settled rows only
- [x] `not_yet_settled` totals the amount and counts rows past their estimate
- [x] `internal/match` does no file IO: slices in, `Report` out

Check: matcher output equals `expected.json` on the generated fixture.

---

## Step 3 — rendering and CLI

- [x] `internal/report`: text renderer, grouped by severity
- [x] `internal/report`: JSON renderer
- [x] `internal/report`: HTML renderer, matching the approved mockup
- [x] `cmd/rekon`: flags, wiring, no logic of its own
- [x] Exit code is non-zero when the money-at-risk group is non-empty
- [x] Detected timezone printed in the header of every format

Check: `rekon testdata/internal.csv testdata/provider.csv` prints the counts, and
`--html` writes a file that opens in a browser.

---

## Step 4 — the local page

- [x] `internal/web`: server bound to `127.0.0.1` on a free port
- [x] `embed` bundles the page, the CSS and the report template into the binary
- [x] Opens the browser on start
- [x] File picker accepts the two files in either order
- [x] Each file is validated as it is chosen, not when the button is pressed
- [x] Reconcile stays disabled until both files are accepted
- [x] Rejected file shows the reason in plain language plus what to do next
- [x] Nothing written to disk, nothing kept after the process exits

Check: the four states in the PRD screen flow all reachable by hand.

---

## Step 5 — tests

- [x] Tier 1 scenarios green against the fixture
- [x] Tier 2 scenarios green against `testdata/real/`
- [x] Tier 3 Playwright specs green against the local page
- [x] Benchmark: 50,000 rows through the matcher
- [x] Coverage above 85 percent on `internal/match`
- [x] Test report rendered to PDF through the report kit

Check: one command runs all three tiers and produces the PDF.

---

## Step 6 — publish

- [x] README, with the required synthetic-data disclaimer sentence
- [x] LICENSE, MIT
- [x] Prior art acknowledged in one paragraph, no comparison table
- [x] Numbers in the README come from step 5, not from estimates
- [x] `reference/` stays git-ignored
- [ ] First push, repo pinned on the profile

---

## Rules that hold throughout

- No third-party module in the shipped binary. Playwright is test tooling and is
  never compiled in
- No float on any path that touches money
- Every deliberate shortcut gets a `ponytail:` comment naming its ceiling
- A claim in the README needs a number produced by a test, not an estimate
