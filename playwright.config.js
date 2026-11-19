// Tier 3 configuration. Playwright builds and starts the binary itself, so the
// only command a contributor needs is `npm test`.
const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  reporter: [['list'], ['json', { outputFile: 'e2e/results.json' }]],
  use: {
    baseURL: 'http://127.0.0.1:8791',
    trace: 'retain-on-failure',
  },
  webServer: {
    // -port keeps the page on a known address; without it the binary picks a
    // free one, which is right for a person and wrong for a test runner.
    command: 'go run ./cmd/rekon -serve -port 8791',
    url: 'http://127.0.0.1:8791',
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
