import { expect, test } from '../fixtures/errors';

/**
 * WI-26 — dark-mode highlight color consistency.
 *
 * The reporter saw three surfaces using different hover backgrounds in
 * dark mode. Exercise two real interactive surfaces so this verifies what
 * a user sees after switching themes, rather than checking the token or DOM
 * implementation directly.
 */

const EXPECTED_DARK_HOVER = /rgba\(166,\s*197,\s*226,\s*0\.3\b/;

test.describe('WI-26: dark-mode highlight consistency', () => {
  test('dark-mode navigation and picker hovers use the same visible highlight', async ({
    page,
    allowConsoleError,
  }) => {
    // Optional services may 404 in the e2e build — irrelevant to this spec.
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/attachment-settings\//);
    allowConsoleError(/Failed to load (buckets|all documents|attachment status)/);

    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    await page.getByTestId('user-avatar-trigger').click();
    await page.getByTestId('theme-menu').click();
    await page.getByTestId('theme-dark').click();
    await page.keyboard.press('Escape');

    const collectionsNav = page.locator('#nav-collections');
    await collectionsNav.hover();
    await expect(collectionsNav).toHaveCSS('background-color', EXPECTED_DARK_HOVER);

    await page.locator('#global-create-button').click();
    const typeChip = page.getByTestId('create-type-chip');
    await typeChip.click();
    const typeOption = page.getByTestId('create-type-chip-option').nth(1);
    await expect(typeOption).toBeVisible();
    await typeOption.hover();
    await expect(typeOption).toHaveCSS('background-color', EXPECTED_DARK_HOVER);
  });
});
