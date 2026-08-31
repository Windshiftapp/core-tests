import { expect, test } from '../fixtures/context-path';
import { createPortalChannel } from '../helpers/portal-setup';

/**
 * Portal channel enable/disable toggle persistence.
 *
 * Regression coverage for the fix in PortalChannelPage.svelte:handleSaveSettings.
 * Pre-fix, the "Enable Portal" toggle would flip in the UI and Save Changes
 * would call api.channels.update(...) (which does not write `status`) without
 * also calling the dedicated api.channels.toggle(id) endpoint — so the new
 * state was never persisted server-side even though the toast claimed success.
 *
 * Both directions are covered because the fix is a single equality branch
 * (portalFormData.enabled !== currentlyEnabled) and the most plausible
 * regression is short-circuiting to only one direction.
 *
 * Verification combines an authoritative API refetch (catches backend
 * divergence) with a page reload + aria-checked assertion (catches a
 * "saved but lies on reload" regression).
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Portal channel enable/disable toggle', () => {
  test('disabled → toggle on → save → status persists as enabled', async ({ page, request }) => {
    const stamp = Date.now();
    const ch = await createPortalChannel(request, {
      slug: `e2e-toggle-on-${stamp}`,
      name: `Toggle On ${stamp}`,
    });

    // Helper creates the channel as 'enabled'. Flip it once to land in a
    // known 'disabled' starting state.
    const seedOff = await request.put(`${BASE_URL}/api/channels/${ch.channelId}/toggle`, {
      headers: SEC_FETCH,
    });
    expect(seedOff.ok(), `seed disable: ${seedOff.status()}`).toBeTruthy();

    await page.goto(`${BASE_URL}/admin/channels/${ch.channelId}/portal`);
    const toggle = page.locator('button[role="switch"]');
    await expect(toggle).toHaveAttribute('aria-checked', 'false');

    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-checked', 'true');

    // The bug skipped this PUT entirely. Waiting on it is the direct
    // signature of the fix — a passing toast alone would have lied pre-fix.
    const [toggleResp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().endsWith(`/api/channels/${ch.channelId}/toggle`) && r.request().method() === 'PUT'
      ),
      page.getByRole('button', { name: /save changes/i }).click(),
    ]);
    expect(toggleResp.ok(), `toggle PUT: ${toggleResp.status()}`).toBeTruthy();

    const after = await request.get(`${BASE_URL}/api/channels/${ch.channelId}`, {
      headers: SEC_FETCH,
    });
    expect(after.ok()).toBeTruthy();
    expect((await after.json()).status).toBe('enabled');

    await page.reload();
    await expect(page.locator('button[role="switch"]')).toHaveAttribute('aria-checked', 'true');
  });

  test('enabled → toggle off → save → status persists as disabled', async ({ page, request }) => {
    const stamp = Date.now();
    const ch = await createPortalChannel(request, {
      slug: `e2e-toggle-off-${stamp}`,
      name: `Toggle Off ${stamp}`,
    });
    // Helper already creates as 'enabled' — no seeding step needed.

    await page.goto(`${BASE_URL}/admin/channels/${ch.channelId}/portal`);
    const toggle = page.locator('button[role="switch"]');
    await expect(toggle).toHaveAttribute('aria-checked', 'true');

    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-checked', 'false');

    const [toggleResp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().endsWith(`/api/channels/${ch.channelId}/toggle`) && r.request().method() === 'PUT'
      ),
      page.getByRole('button', { name: /save changes/i }).click(),
    ]);
    expect(toggleResp.ok(), `toggle PUT: ${toggleResp.status()}`).toBeTruthy();

    const after = await request.get(`${BASE_URL}/api/channels/${ch.channelId}`, {
      headers: SEC_FETCH,
    });
    expect(after.ok()).toBeTruthy();
    expect((await after.json()).status).toBe('disabled');

    await page.reload();
    await expect(page.locator('button[role="switch"]')).toHaveAttribute('aria-checked', 'false');
  });
});
