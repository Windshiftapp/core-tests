import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';

/**
 * AI chat entry points are gated on aiStore.chatAvailable, which is hydrated
 * from a single shell-bootstrap snapshot at app mount. The admin area is a view
 * inside that same shell, so configuring the first LLM connection used to leave
 * the snapshot stale: chat stayed hidden until the user reloaded the browser
 * (WI-857).
 *
 * This spec drives the real admin form so the shared post-admin-mutation shell
 * refresh is exercised, then asserts the nav entry appears and disappears
 * without any navigation or reload in between.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

async function listConnections(request: APIRequestContext) {
  const resp = await request.get(`${BASE_URL}/api/admin/llm-connections`, {
    headers: defaultHeaders,
  });
  expect(
    resp.ok(),
    `list llm connections failed (${resp.status()}): ${await resp.text()}`
  ).toBeTruthy();
  return (await resp.json()) ?? [];
}

/** Leave no enabled connection behind — availability is global admin state. */
async function deleteAllConnections(request: APIRequestContext) {
  for (const conn of await listConnections(request)) {
    const resp = await request.delete(`${BASE_URL}/api/admin/llm-connections/${conn.id}`, {
      headers: defaultHeaders,
    });
    expect(resp.ok(), `delete llm connection ${conn.id} failed (${resp.status()})`).toBeTruthy();
  }
}

test.describe('AI chat availability follows LLM connection config', () => {
  // Serial: these mutate instance-wide AI availability.
  test.describe.configure({ mode: 'serial' });

  test.beforeEach(async ({ request }) => {
    await deleteAllConnections(request);
  });

  test.afterEach(async ({ request }) => {
    await deleteAllConnections(request);
  });

  test('chat entry point appears on create and disappears on delete, without a reload', async ({
    page,
    request,
  }) => {
    const name = `e2e-chat-availability-${Date.now()}`;
    const chatButton = page.locator('#chat-toggle-button');

    await page.goto('/admin/llm-connections');
    await expect(page.locator('#llm-connection-add')).toBeVisible();

    // No enabled connection yet, so the shell must offer no way into chat.
    await expect(chatButton).toHaveCount(0);

    await page.locator('#llm-connection-add').click();
    await page.locator('#llm-connection-name').fill(name);

    // The Select menu is portalled out of the trigger, so scope by its option testid.
    await page.locator('#llm-connection-provider').click();
    await page
      .locator('[data-testid="llm-connection-provider-option"][data-option-id="openai"]')
      .click();

    await page.locator('#llm-connection-api-key').fill('sk-e2e-not-a-real-key');

    // Model is a create-allowed picker: type an id and take the create option
    // rather than depending on a refreshed provider catalog.
    await page.locator('#llm-connection-model').click();
    await page.locator('#llm-connection-model').pressSequentially('gpt-4o-mini');
    await page.locator('[data-testid="picker-create-option"]').click();

    await page.locator('#llm-connection-create-submit').click();

    // The assertion that matters: same page, no reload, no navigation.
    await expect(chatButton).toBeVisible();

    // The connection landed enabled, which is what availability keys off.
    const [created] = await listConnections(request);
    expect(created.name).toBe(name);
    expect(created.is_enabled).toBe(true);

    // Removing the last connection must retract the entry point just as promptly.
    await page.locator('[data-testid="llm-connection-delete"]').click();
    await page.locator('[data-testid="dialog-confirm"]').click();

    await expect(chatButton).toHaveCount(0);
  });
});
