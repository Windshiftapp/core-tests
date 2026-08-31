import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';

/**
 * WI-37: "Agents in comments do not identify their owner"
 *
 * The Bot icon next to an agent comment must expose the agent's owner so
 * the reader can trace the comment back to the human who provisioned the
 * agent. Verifies the new GET /api/users/{id}/agent-owner endpoint plus
 * the Comments.svelte tooltip wiring.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

async function createAgent(request: APIRequestContext, suffix: string) {
  const res = await request.post(`${BASE_URL}/api/me/agents`, {
    headers: defaultHeaders,
    data: {
      username: `agent-wi37-${suffix}-${Date.now()}`,
      first_name: 'WI37',
      last_name: 'Agent',
    },
  });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function mintAgentToken(
  request: APIRequestContext,
  agentId: number,
  name: string
): Promise<string> {
  const res = await request.post(`${BASE_URL}/api/api-tokens`, {
    headers: defaultHeaders,
    data: { name, user_id: agentId, permissions: ['items:write'] },
  });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  // CreateToken returns the plaintext token alongside the persisted row.
  return body.plaintext_token || body.token || body.api_token?.plaintext_token;
}

test.describe('WI-37: agent comments expose owner via tooltip', () => {
  let agentId: number;

  test.afterAll(async ({ request }) => {
    if (agentId) {
      await request.delete(`${BASE_URL}/api/me/agents/${agentId}`, {
        headers: defaultHeaders,
      });
    }
  });

  test('comment authored by an agent surfaces the owner name', async ({
    page,
    request,
    playwright,
  }) => {
    const agent = await createAgent(request, 'tooltip');
    agentId = agent.id;
    const agentToken = await mintAgentToken(request, agentId, `wi37-${Date.now()}`);
    expect(agentToken, 'agent token must be returned in plaintext on mint').toBeTruthy();

    // Workspace + item the admin owns; the agent inherits visibility from
    // its owner (E2E Admin) so it can comment on items it can already see.
    const ws = await createWorkspaceViaAPI(request, {
      name: `WI-37 ${Date.now()}`,
      key: `W37${Date.now().toString().slice(-5)}`,
      description: 'WI-37 fixture',
    });
    const item = await createItemViaAPI(request, ws.id, {
      title: 'WI-37 item',
    });

    // Post the comment as the agent. Agent API tokens only authenticate on
    // /rest/api/v1/*, so use the v1 endpoint here rather than /api/items/...
    const agentCtx = await playwright.request.newContext({
      baseURL: BASE_URL,
      extraHTTPHeaders: { Authorization: `Bearer ${agentToken}` },
      storageState: { cookies: [], origins: [] },
    });
    const commentRes = await agentCtx.post(`/rest/api/v1/items/${item.id}/comments`, {
      data: { content: 'WI-37 agent comment' },
    });
    expect(commentRes.ok(), 'agent must be able to post a comment').toBeTruthy();

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);

    // Wait for the comment to render — its testid hangs off Comments.svelte.
    const commentRow = page.locator('[data-testid="comment-item"]').filter({
      hasText: 'WI-37 agent comment',
    });
    await expect(commentRow).toBeVisible({ timeout: 15000 });

    // The Bot icon next to the author name carries the tooltip we care about.
    // Tooltip content is exposed via the wrapper's `data-tooltip` / aria-label
    // depending on the implementation; assert against the rendered title text
    // after hovering, which works for both popper- and CSS-based tooltips.
    const botBadge = commentRow.locator('svg.lucide-bot').first();
    await expect(botBadge).toBeVisible();

    await botBadge.hover();
    // The owner of the agent in this test is the e2e admin: "E2E Admin".
    await expect(page.getByText(/AI agent owned by E2E Admin/i)).toBeVisible({ timeout: 5000 });
  });
});
