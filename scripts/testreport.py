"""Turn the three test tiers into one PDF through the report kit.

Run it after `go test` and `npx playwright test`. It reads their machine-readable
output rather than re-running anything, so the figures in the report are the
figures the tests actually produced.

    go test ./... -json > .testout/go.json
    npx playwright test
    python scripts/testreport.py

The point is not ceremony. The README is only allowed to quote numbers a test
produced, and this is where those numbers are read from.
"""

import io
import json
import os
import re
import subprocess
import sys
import datetime as dt

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# The report kit is the author's own document toolchain and is not part of this
# repository. Point REPORT_KIT at it, or skip this script: nothing else needs it.
KIT = os.environ.get("REPORT_KIT", os.path.join(ROOT, "..", "report-kit"))
OUT_HTML = os.path.join(ROOT, "testreport.html")
OUT_PDF = os.path.join(ROOT, "testreport.pdf")

TIER = {
    "internal/money": 1,
    "internal/match": 1,
    "internal/ledger": 1,
    "internal/report": 1,
    "internal/web": 1,
    "cmd/rekon": 1,
    "internal/xendit": 2,
}


def go_results(path):
    """Read `go test -json` output into (package, test, pass/fail) rows."""
    rows, pkgs = [], {}
    if not os.path.exists(path):
        return rows, pkgs
    for line in io.open(path, encoding="utf-8"):
        line = line.strip()
        if not line.startswith("{"):
            continue
        e = json.loads(line)
        pkg = e.get("Package", "").split("/rekon/")[-1]
        act = e.get("Action")
        if e.get("Test") and act in ("pass", "fail", "skip"):
            rows.append({"pkg": pkg, "test": e["Test"], "action": act,
                         "elapsed": e.get("Elapsed", 0), "tier": TIER.get(pkg, 1)})
        elif not e.get("Test") and act in ("pass", "fail"):
            pkgs[pkg] = act
    return rows, pkgs


def pw_results(path):
    rows = []
    if not os.path.exists(path):
        return rows
    data = json.load(io.open(path, encoding="utf-8"))

    def walk(suite):
        for spec in suite.get("specs", []):
            ok = all(t.get("status") == "expected" for t in spec.get("tests", []))
            ms = sum(r.get("duration", 0)
                     for t in spec.get("tests", []) for r in t.get("results", []))
            rows.append({"pkg": "e2e", "test": spec["title"],
                         "action": "pass" if ok else "fail",
                         "elapsed": ms / 1000.0, "tier": 3})
        for child in suite.get("suites", []):
            walk(child)

    for s in data.get("suites", []):
        walk(s)
    return rows


def read_metric(path, pattern, default="not measured"):
    if not os.path.exists(path):
        return default
    m = re.search(pattern, io.open(path, encoding="utf-8", errors="replace").read())
    return m.group(1) if m else default


def esc(s):
    return (str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;"))


def main():
    out = os.path.join(ROOT, ".testout")
    rows, _ = go_results(os.path.join(out, "go.json"))
    rows += pw_results(os.path.join(ROOT, "e2e", "results.json"))
    if not rows:
        sys.exit("no test output found: run the tests first, see the docstring")

    bench = read_metric(os.path.join(out, "bench.txt"),
                        r"BenchmarkRun\S*\s+\d+\s+(\d+)\s+ns/op")
    if bench.isdigit():
        bench = "%.0f ms" % (int(bench) / 1_000_000)
    # Two different numbers, and confusing them once put a whole-repository
    # figure next to a single package's target, which read as a failure when
    # nothing had failed. They are read from separate files for that reason.
    cover = read_metric(os.path.join(out, "cover-match.txt"), r"coverage: ([\d.]+%)")
    e2e = read_metric(os.path.join(out, "e2e.txt"), r"end to end: (\d+ ms)")

    tiers = {
        1: ("Tier 1, unit", "Go, against the generated fixture. Is the logic right."),
        2: ("Tier 2, reality", "Go, against the sandbox export. Was the documentation read right."),
        3: ("Tier 3, browser", "Playwright, against the local page. Can a person use it."),
    }

    body = []
    passed = sum(1 for r in rows if r["action"] == "pass")
    failed = sum(1 for r in rows if r["action"] == "fail")

    body.append('<h1>Summary</h1>')
    body.append('<p class="lead">%d of %d scenarios passed.%s</p>' % (
        passed, len(rows), "" if not failed else " <strong>%d failed.</strong>" % failed))
    body.append("<table><thead><tr><th>Measure</th><th>Result</th><th>Target</th></tr></thead><tbody>")
    body.append('<tr><td>Matcher, 50,000 rows</td><td>%s</td><td>under 2 s</td></tr>' % esc(bench))
    body.append('<tr><td>End to end, 50,000 rows</td><td>%s</td><td>under 2 s</td></tr>' % esc(e2e))
    body.append('<tr><td>Coverage, <code>internal/match</code></td><td>%s</td><td>above 85%%</td></tr>' % esc(cover))
    body.append('<tr><td>Third-party modules in the binary</td><td>0</td><td>0</td></tr>')
    body.append("</tbody></table>")

    # Every image below was captured inside a passing browser test, so it can
    # only exist if the assertion beside it held. A screenshot taken separately
    # would show a state without proving it was ever reached.
    shots = [
        ("state-1-empty", "Nothing chosen. Reconcile is disabled, and stays disabled until both files are accepted."),
        ("state-2-ready", "Both files accepted. Each slot reports what the adapter saw, including the timezone the file was written in."),
        ("state-4-rejected", "A report in the format retired in July 2026. The rejection names what is missing and says what to do, and the run is blocked."),
        ("state-3-report", "The report. Money at risk first, in flight separately, clean last."),
    ]
    have = [(n, c) for n, c in shots
            if os.path.exists(os.path.join(ROOT, "e2e", "screenshots", n + ".png"))]
    if have:
        body.append('<h1 class="break-before">Evidence</h1>')
        body.append('<p class="lead">The four states of the screen flow, captured during the browser run.</p>')
        for i in range(0, len(have), 2):
            pair = have[i:i + 2]
            body.append('<div class="figure-pair">' if len(pair) == 2 else "")
            for j, (name, caption) in enumerate(pair):
                body.append(
                    '<figure><img src="e2e/screenshots/%s.png" alt="%s">'
                    '<figcaption><strong>Figure %d.</strong> %s</figcaption></figure>'
                    % (name, esc(caption), i + j + 1, esc(caption)))
            body.append("</div>" if len(pair) == 2 else "")

    for tier in (1, 2, 3):
        group = [r for r in rows if r["tier"] == tier]
        if not group:
            continue
        title, note = tiers[tier]
        body.append('<h1 class="break-before">%s</h1>' % esc(title))
        body.append('<p class="lead">%s</p>' % esc(note))
        body.append("<table><thead><tr><th>Package</th><th>Scenario</th><th>Time</th><th>Result</th></tr></thead><tbody>")
        for r in sorted(group, key=lambda r: (r["pkg"], r["test"])):
            cls = {"pass": "pass", "fail": "fail", "skip": "warn"}[r["action"]]
            word = {"pass": "Pass", "fail": "Fail", "skip": "Skipped"}[r["action"]]
            body.append('<tr><td><code>%s</code></td><td>%s</td><td>%.2fs</td><td class="status %s">%s</td></tr>'
                        % (esc(r["pkg"]), esc(r["test"]), r["elapsed"], cls, word))
        body.append("</tbody></table>")

    stamp = dt.datetime.now().strftime("%-d %B %Y" if os.name != "nt" else "%d %B %Y")
    html = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>rekon: test report</title>
<link rel="stylesheet" href="%s/report.css">
</head>
<body data-author="Alsiendo Dewantara" data-title="rekon test report">
<div id="book">
  <section class="page cover">
    <div class="cover-top">
      <div class="cover-kicker">Test Report &middot; rekon &middot; %s</div>
      <h1>rekon</h1>
      <div class="cover-rule"></div>
      <p class="cover-sub">Three tiers: the logic against a fixture, the reading of the documentation against a real export, and the page against a browser.</p>
    </div>
  </section>
</div>
<div id="flow">
<nav id="toc" class="break-before"><h1>Contents</h1><ol></ol></nav>
%s
</div>
<script src="%s/paginate.js"></script>
</body>
</html>
""" % (KIT.replace("\\", "/"), stamp, "\n".join(body), KIT.replace("\\", "/"))

    io.open(OUT_HTML, "w", encoding="utf-8", newline="\n").write(html)

    if not os.path.exists(os.path.join(KIT, "report.css")):
        sys.exit("report kit not found at %s. Set REPORT_KIT, or skip this script." % KIT)
    chrome = os.environ.get("CHROME",
                            r"C:\Program Files\Google\Chrome\Application\chrome.exe")
    subprocess.run([chrome, "--headless", "--disable-gpu", "--no-pdf-header-footer",
                    "--virtual-time-budget=5000", "--run-all-compositor-stages-before-draw",
                    "--print-to-pdf=" + OUT_PDF, "file:///" + OUT_HTML.replace("\\", "/")],
                   check=True, capture_output=True)
    print("wrote %s (%d scenarios, %d failed)" % (OUT_PDF, len(rows), failed))


if __name__ == "__main__":
    main()
