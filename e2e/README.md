# Tier 3 tests

These drive the local page in a real browser. Everything else in the repository
runs with Go alone; this directory is the one place that needs Node.

    npm install
    npx playwright install chromium
    npm test

`playwright.config.js` builds and starts the binary itself, so there is no
server to start by hand.

`fixtures/retired-format.csv` is the Xendit report format that was deprecated on
9 July 2026. It is kept so the rejection path has something real to reject: it
has no Timezone column, and its withholding column carries the old name.
