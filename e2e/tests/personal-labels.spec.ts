import { expect, test } from '../fixtures/context-path';

/**
 * The browser contract for the Labels tab lives here. The personal-label CRUD,
 * colour normalization, listing, and item-association HTTP contracts live in
 * tests/e2e_security_contracts_test.go so this suite exercises the UI outcome.
 */

test.describe('Personal labels — Profile tab UI smoke', () => {
  test('Labels tab loads the manager and shows the new-label affordance', async ({ page }) => {
    // The /profile page bootstraps several effects on mount (auth, agents,
    // attachment status, regional settings). On a cold first server start
    // the JS bundle parse + first paint can lag behind Playwright's
    // auto-wait, so a naive .goto().click() races the Tabs click handler
    // before it's bound. Wait for the agents fetch (the same readiness
    // signal used by tests/agent-token-revoke.spec.ts) before clicking,
    // then for full network idle so any deferred Svelte effects have
    // settled. This mirrors a real user's interaction timing.
    const agentsResponse = page.waitForResponse(
      (res) => res.url().endsWith('/api/me/agents') && res.ok()
    );
    await page.goto('/profile');
    await agentsResponse;
    await page.waitForLoadState('networkidle');

    const labelsTab = page.getByTestId('profile-tab-labels');
    await expect(labelsTab).toBeVisible();
    await labelsTab.click();

    // The manager always renders this button at the top of the panel
    // regardless of whether the labels list has finished loading.
    await expect(page.getByTestId('personal-label-new')).toBeVisible();
  });
});
