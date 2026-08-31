import { expect, test } from '../fixtures/context-path';

/**
 * WI-51: the "A" and "T" keyboard hints on the Security page must actually
 * open the Add Credential and Create Token modals. Previously only the
 * `keyboardHint` badge was set on the buttons; the `hotkeyConfig` binding
 * was missing so the keys did nothing.
 */

const DIALOG = '[role="dialog"]';

test.describe('Security page shortcuts', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/security');
    await page.waitForLoadState('networkidle');
    // The page header is the deterministic settle signal — wait for it
    // before pressing keys.
    await expect(page.getByRole('heading', { name: /security/i }).first()).toBeVisible();
  });

  test('"a" opens the Add Security Credential modal', async ({ page }) => {
    await expect(page.locator(DIALOG)).toHaveCount(0);

    await page.locator('body').press('a');

    const dialog = page.locator(DIALOG);
    await expect(dialog).toBeVisible({ timeout: 3000 });
    await expect(dialog.getByRole('heading', { name: 'Add Security Credential' })).toBeVisible();
  });

  test('"t" opens the Create Token modal', async ({ page }) => {
    await expect(page.locator(DIALOG)).toHaveCount(0);

    await page.locator('body').press('t');

    const dialog = page.locator(DIALOG);
    await expect(dialog).toBeVisible({ timeout: 3000 });
    await expect(dialog.getByRole('heading', { name: 'Create Token' })).toBeVisible();
  });
});
