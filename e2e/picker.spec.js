// Tier 3. These drive the local page in a real browser.
//
// Go's net/http/httptest can exercise the handlers, and it does, one layer down.
// What it cannot do is press a disabled button, watch an error state render, or
// work a file picker. Those are the things a finance operator actually touches,
// so they are tested where they live.
//
// Playwright is test tooling. It is never imported by the program and never
// compiled into the binary.

const { test, expect } = require('@playwright/test');
const path = require('path');

const fixture = (name) => path.join(__dirname, '..', 'testdata', name);

// Screenshots are captured inside passing tests, so an image in the test report
// can only exist if the assertion next to it held. A picture taken by a separate
// script would prove nothing about whether the state was ever reached.
const shot = (page, name) =>
  page.screenshot({ path: path.join(__dirname, 'screenshots', name + '.png'), fullPage: true });
const OLD_FORMAT = path.join(__dirname, 'fixtures', 'retired-format.csv');

test.use({ viewport: { width: 900, height: 760 } });

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

// Scenario 18. State 1 of the screen flow.
test('Reconcile stays disabled until both files are accepted', async ({ page }) => {
  await expect(page.locator('#go')).toBeDisabled();
  await shot(page, 'state-1-empty');

  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  await expect(page.locator('#slot-a')).toHaveClass(/filled/);
  await expect(page.locator('#go')).toBeDisabled();

  await page.setInputFiles('input[name=b]', fixture('internal.csv'));
  await expect(page.locator('#go')).toBeEnabled();
});

// Scenario 19. The header decides which slot a file belongs to, so an operator
// cannot put them in the wrong way round.
test('files chosen in either order land in the correct slot', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('internal.csv'));
  await page.setInputFiles('input[name=b]', fixture('provider.csv'));

  await expect(page.locator('#slot-a .t1')).toContainText('Internal orders');
  await expect(page.locator('#slot-b .t1')).toContainText('Provider report');
  await expect(page.locator('#go')).toBeEnabled();
  await shot(page, 'state-2-ready');
});

// The slot reports what the adapter saw, so the operator can check the timezone
// before committing to a run.
test('an accepted provider file reports its rows and timezone', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  const summary = page.locator('#slot-a .t2');
  await expect(summary).toContainText('payments');
  await expect(summary).toContainText('+0700');
  await expect(summary).toContainText('not payments');
});

// Scenario 20. State 4. The rejection is written for a person, and the run is
// blocked rather than allowed to produce a report nobody can trust.
test('a retired-format file is rejected in plain language and blocks the button', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  await page.setInputFiles('input[name=b]', OLD_FORMAT);

  await expect(page.locator('#slot-b')).toHaveClass(/bad/);
  await expect(page.locator('#slot-b .t2')).toContainText('not a current Xendit transaction report');
  await expect(page.locator('#fix')).toBeVisible();
  await expect(page.locator('#fix-text')).toContainText('July 2026');
  await expect(page.locator('#go')).toBeDisabled();
  await shot(page, 'state-4-rejected');
});

// Scenario 21. State 4 back to state 2, without a reload. The rejected slot is a
// state of the picker, not a separate page to navigate back from.
test('replacing the rejected file re-enables the button without a reload', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  await page.setInputFiles('input[name=b]', OLD_FORMAT);
  await expect(page.locator('#go')).toBeDisabled();

  await page.setInputFiles('input[name=b]', fixture('internal.csv'));
  await expect(page.locator('#slot-b')).toHaveClass(/filled/);
  await expect(page.locator('#fix')).toBeHidden();
  await expect(page.locator('#go')).toBeEnabled();
});

// Two files of the same kind cannot be reconciled against each other.
test('two provider files are refused as a pair', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  await page.setInputFiles('input[name=b]', fixture('provider.csv'));

  await expect(page.locator('#fix-text')).toContainText('same kind');
  await expect(page.locator('#go')).toBeDisabled();
});

// Scenario 22. State 3. The report leads with money, not with row counts.
test('the report shows the money at risk and the in-flight amount', async ({ page }) => {
  await page.setInputFiles('input[name=a]', fixture('provider.csv'));
  await page.setInputFiles('input[name=b]', fixture('internal.csv'));
  await page.click('#go');

  await expect(page.locator('.verdict .amt')).toContainText('IDR');
  await expect(page.locator('.verdict .lbl')).toContainText('at risk across');

  await expect(page.locator('section.money-at-risk')).toContainText('Paid, never settled');
  await expect(page.locator('section.money-at-risk')).toContainText('Settled twice');
  await expect(page.locator('section.in-flight-nothing-wrong')).toContainText('settlement pending');
  await expect(page.locator('.note-band')).toContainText('waiting');
  await expect(page.locator('.clean')).toContainText('transactions');
  await shot(page, 'state-3-report');
});

// The page states plainly that nothing left the machine. Finance is handing it
// settlement data; the reassurance belongs on screen, not in a README.
test('the page says the files stay on this computer', async ({ page }) => {
  await expect(page.locator('.hint')).toContainText('stay on this computer');
});
