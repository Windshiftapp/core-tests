import { type APIRequestContext, expect, test } from '../fixtures/context-path';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * WI-11: "Token gets revoked immediately".
 *
 * On /profile → Agents, clicking "Revoke Token" used to call the API
 * before the confirmation dialog opened, because the click handler used
 * `if (!confirm('…')) return` — but `confirm` here is the custom
 * Promise-returning helper from useConfirm.js, not window.confirm, so
 * the Promise was always truthy, the early-return never fired, and the
 * token disappeared first; the dialog then appeared after the fact.
 *
 * The fix awaits the confirmation. This spec pins that behavior.
 */

async function createAgent(request: APIRequestContext, suffix: string) {
  const res = await request.post(`${BASE_URL}/api/me/agents`, {
    headers: defaultHeaders,
    data: {
      username: `agent-${suffix}-${Date.now()}`,
      first_name: 'Agent',
      last_name: suffix,
    },
  });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function mintToken(request: APIRequestContext, agentId: number, name: string) {
  const res = await request.post(`${BASE_URL}/api/api-tokens`, {
    headers: defaultHeaders,
    data: { name, user_id: agentId, permissions: [] },
  });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

test.describe('Agent token revoke confirmation — WI-11', () => {
  test.describe.configure({ mode: 'serial' });

  let agentId: number;
  let tokenId: number;
  let tokenName: string;

  test.beforeEach(async ({ request }) => {
    const agent = await createAgent(request, 'wi11');
    agentId = agent.id;

    tokenName = `wi11-token-${Date.now()}`;
    const mint = await mintToken(request, agentId, tokenName);
    tokenId = mint.api_token.id;
  });

  test.afterEach(async ({ request }) => {
    // Delete the agent so retries / later tests don't see stale rows.
    if (agentId) {
      await request.delete(`${BASE_URL}/api/me/agents/${agentId}`, {
        headers: defaultHeaders,
      });
    }
  });

  async function openAgentsTab(page) {
    // UserProfile loads agents from a $effect gated on currentUserId; on a
    // fresh page that fires after the initial render, so the tab can open
    // with an empty list before the response lands. The shell bootstrap also
    // controls whether the avatar tab exists, so wait for both before
    // interacting with the tab strip and avoid a late layout shift.
    const agentsResponse = page.waitForResponse(
      (res) => res.url().endsWith('/api/me/agents') && res.ok()
    );
    const shellBootstrapResponse = page.waitForResponse(
      (res) => res.url().endsWith('/api/shell-bootstrap') && res.ok()
    );
    await page.goto('/profile');
    await Promise.all([agentsResponse, shellBootstrapResponse]);

    // Use the stable button id and keyboard activation so a changing tab width
    // cannot redirect a coordinate-based click to the adjacent tab.
    const agentsTab = page.getByTestId('profile-tab-agents');
    await agentsTab.focus();
    await agentsTab.press('Enter');
    await expect(page.getByRole('heading', { name: 'Agents', exact: true })).toBeVisible();

    // Scope to the agent we just created — tests create fresh agents and
    // the tab lists every agent we own, so an unscoped selector can match
    // more than one row.
    const agentRow = page.getByTestId(`agent-row-${agentId}`);
    await expect(agentRow).toBeVisible();

    // Wait for the tokens XHR that "Manage tokens" triggers before asserting
    // on the row — otherwise we can race the list render.
    const tokensResponse = page.waitForResponse(
      (res) => res.url().includes(`/api/api-tokens?user_id=${agentId}`) && res.ok()
    );
    await page.getByTestId(`agent-actions-${agentId}`).click();
    await page.getByTestId(`agent-manage-tokens-${agentId}`).click();
    await tokensResponse;
    await expect(page.getByTestId(`agent-token-row-${tokenId}`)).toBeVisible();
  }

  test('clicking revoke opens a confirm dialog before calling the API', async ({ page }) => {
    await openAgentsTab(page);

    // Tripwire: if the API is hit before the dialog resolves, the bug is back.
    let revokeApiCalled = false;
    await page.route(`**/api/api-tokens/${tokenId}`, async (route) => {
      if (route.request().method() === 'DELETE') {
        revokeApiCalled = true;
      }
      await route.continue();
    });

    await page.getByTestId(`agent-token-revoke-${tokenId}`).click();

    // Dialog is up; API has NOT been called yet.
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    expect(revokeApiCalled).toBe(false);
    await expect(page.getByTestId(`agent-token-row-${tokenId}`)).toBeVisible();
  });

  test('cancel in the confirm dialog keeps the token', async ({ page }) => {
    await openAgentsTab(page);

    await page.getByTestId(`agent-token-revoke-${tokenId}`).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();

    await page.getByTestId('dialog-cancel').click();
    await expect(dialog).not.toBeVisible();

    // Token row still there.
    await expect(page.getByTestId(`agent-token-row-${tokenId}`)).toBeVisible();
  });

  test('confirm in the dialog revokes the token', async ({ page }) => {
    await openAgentsTab(page);

    await page.getByTestId(`agent-token-revoke-${tokenId}`).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();

    await page.getByTestId('dialog-confirm').click();
    await expect(page.getByTestId(`agent-token-row-${tokenId}`)).toHaveCount(0);
  });
});
