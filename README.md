# rekon

Settlement reconciliation for Indonesian payment providers. Two CSV files in,
one report out, and it says exactly where the two disagree.

Reconciliation tooling exists. What it generally does not do is model a specific
provider's fee arithmetic, settlement states and timezone behaviour, which is
where the false positives come from. This does that for Xendit.

```
rekon internal.csv provider.csv
```

```
  adapter    xendit transaction report
  timezone   +0700
  rows       4,976 internal   4,999 provider   3 skipped


  MONEY AT RISK                                            98
    missing_in_provider       29   Marked paid internally. The provider has no such transaction.
    missing_in_internal       24   Money received that cannot be attributed to an order.
    duplicate                 25   Same reference paid out more than once. Expect a clawback.
    amount_mismatch           20   Gross on the provider row differs from the order.
                           IDR 889,626,811.00


  WORTH CHECKING                                           24
    fee_mismatch              24   Net is not gross minus fee, VAT and withholding tax.
                           IDR 24,000.00


  IN FLIGHT, NOTHING WRONG                                 26
    not_yet_settled           26   The customer paid. The money has not landed yet.
                           IDR 184,762,431.00

    oldest waiting 7 days, since 1 Sep
    4 past the provider estimate


  CLEAN                                                 4,852

  98 rows need action, IDR 889,626,811.00 at risk.
```

Run it with no arguments and it opens a page in your browser instead, with a
file picker. Finance does not have to open a terminal.

---

## Why this is harder than a spreadsheet lookup

Every morning someone downloads the provider report, exports the day's orders,
and compares them in Excel. The comparison fails in a specific way, and the
usual fix makes it worse.

**The two amount columns are not the same amount.** The order says 116,601. The
provider reports 111,051, because it deducts a fee of 5,000 and VAT of 550 on
that fee. Subtracting one from the other flags every healthy row. The operator
adds a tolerance to stop the noise, the tolerance has to clear the largest fee
in the file, and from then on any shortfall smaller than that passes silently.
The spreadsheet does not break. It quietly stops detecting the thing it was
built to detect.

`rekon` never uses a tolerance. It splits one fuzzy comparison into two exact
ones: gross against gross, then the four-term fee identity.

**The same instant falls on two calendar days.** A payment at 21:35 UTC is a
4 September payment in Jakarta and a 3 September payment in UTC. In the
committed fixture, 1,706 of 4,996 payments carry a WIB date that differs from
their UTC date, one row in three. An operator filtering to a single day sees a
transaction appear from nowhere on one day and its counterpart go missing from
the other, and the two never look related.

**Fee columns are blank until a row settles.** The provider does not know the
fee until the money moves, so an unsettled row carries no fee, no VAT and no
net. Read as zero rather than absent, every payment still in flight becomes a
fee discrepancy.

**Succeeded is not settled.** `Status` and `Settlement Status` are independent
columns. A payment that went through but has not landed is not a discrepancy,
and reporting it as one trains people to ignore the report.

## What it reports

Seven classes, assigned by a precedence that puts money at risk ahead of
bookkeeping noise.

| Class | Meaning |
|---|---|
| `missing_in_provider` | Marked paid internally, the provider has no such transaction |
| `missing_in_internal` | The provider settled money no order accounts for |
| `duplicate` | The same reference settled more than once |
| `amount_mismatch` | Gross disagrees between the two sides |
| `fee_mismatch` | Net breaks the four-term identity. Settled rows only |
| `not_yet_settled` | Paid, still in flight. Reported with its amount and its age |
| `matched` | Clean |

`not_yet_settled` also counts how many rows are past the provider's own
`Estimated Settlement Date`. Those are the ones worth raising; the rest are
simply not due yet.

## Install and run

Go 1.22 or newer, nothing else.

```
go install github.com/cicilicienye323/rekon/cmd/rekon@latest
```

```
rekon internal.csv provider.csv              print the report
rekon internal.csv provider.csv -json        machine-readable, for a cron job
rekon internal.csv provider.csv -html out.html
rekon                                        open the local page in a browser
```

The two files may be given in either order: which is which is worked out from
their headers. The exit code is 1 when anything sits in the money-at-risk group,
so a scheduled job can notice without parsing the output.

Try it on the committed fixture without supplying anything of your own:

```
git clone https://github.com/cicilicienye323/rekon && cd rekon
go run ./cmd/rekon testdata/internal.csv testdata/provider.csv
```

## Input formats

**Provider.** A Xendit Transaction Report, as exported from the dashboard.
Either the 29-column default or the full 92 columns; the extras are ignored. The
format that was deprecated on 9 July 2026 is refused by name, because it does not
say which timezone its timestamps are in.

**Internal.** Your own order export, with these columns:

```
order_id,reference,gross,currency,paid_at,status
```

`reference` is the join key: it is the `external_id` your system sent to the
provider when the transaction was created.

Adding a second provider means writing one file shaped like `internal/xendit`,
about two hundred lines. Nothing downstream changes.

## Design

**No database.** Two files in, one report out, nothing to persist between runs.
Keep the JSON output and the filesystem is the history.

**No provider API.** An API means credentials, and credentials mean you cannot
clone this and run it on the sample file in thirty seconds.

**No fuzzy matching.** If the reference is absent, that is itself the finding. A
wrong pair is worse than an unmatched row, because nobody looks at it again.

**No rate-card verification.** `rekon` checks that a row is internally
consistent, not that the rate matches a contract. Rate cards are commercially
confidential and differ per merchant, so hardcoding one would make the tool
wrong for everybody else. That decision was made before a real export showed the
fee shape varies by channel anyway: virtual accounts charge a flat fee, cards
and wallets charge a percentage. The four-term identity holds either way.

**No float on any path that touches money.** Amounts are integer minor units,
scaled by the currency's ISO 4217 exponent. An ambiguous separator is refused
rather than guessed: Indonesian locale writes decimals with a comma, so `1,50`
could mean either one and a half or one hundred and fifty.

## Testing

Three tiers, 73 scenarios, run with `go test ./...` and `npm test`.

| Tier | Runs against | Answers |
|---|---|---|
| 1, Go | The generated fixture | Is the logic right |
| 2, Go | A real sandbox export | Was the documentation read right |
| 3, Playwright | The local page in a browser | Can a person actually use it |

Tier 2 is not optional. The fixture generator and the matcher were written by
the same author from the same reading of the documentation, so a misreading
produces a fixture and a matcher that agree with each other and disagree with
reality. Checking against a real export found three published claims to be
wrong: the withholding column has a different name, the timezone column carries
a zone name rather than an offset, and the money columns are blank until
settlement. All three passed tier 1.

Playwright is test tooling. It is never imported and never compiled in; the
shipped binary has zero third-party dependencies.

| Measure | Result | Target |
|---|---|---|
| Matcher, 50,000 rows | 67 ms | under 2 s |
| End to end, 50,000 rows | 484 ms | under 2 s |
| Coverage, `internal/match` | 93.7% | above 85% |
| Third-party modules in the binary | 0 | 0 |

The number worth quoting is not any of those. It is that 1,706 rows whose
calendar dates disagree produce zero findings. Detecting a planted defect is
easy; staying quiet about a third of the file that merely looks wrong is the
part that required reading the documentation.

## About the figures

Figures come from the committed synthetic fixture with illustrative pricing, not
from a real merchant account. The fee rate, the fee floor and the transaction
size distribution were chosen to demonstrate a shape, not measured from anyone's
integration.

The only real data in this repository is `testdata/real/`, a Xendit test-mode
export from the author's own sandbox account. It contains fictional money and no
customer data.

## Licence

MIT.
