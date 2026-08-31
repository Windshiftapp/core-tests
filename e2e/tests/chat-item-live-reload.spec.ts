import { createItemViaAPI, createWorkspaceViaAPI, updateItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

/**
 * Instant-refresh contract for work item detail: when the AI chat agent
 * finishes a run (chatStore → agentRuns.emit), the open item detail must
 * refetch and rerender immediately, well before the 30s adaptive poller
 * would fire.
 *
 * The chat response is stubbed at the network boundary so the test remains
 * deterministic without requiring an external LLM. The user-visible workflow
 * still drives the real chat panel, which is what produces the completion
 * signal consumed by the item detail:
 *
 *   1. Creating an item via API and opening it.
 *   2. Mutating the title server-side (same write path update_item uses).
 *   3. Opening chat, typing a message, and submitting it.
 *   4. Asserting the new title shows up quickly.
 */

test.describe('Chat live-reload of work item detail', () => {
  test('item detail refetches when agentRuns signals an agent completion', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    // Item-detail load always probes /api/items/:id/recurrence; a 404 here
    // means "no recurrence rule configured", which is the normal case.
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `chat-item-live-reload-${stamp}`,
      key: `CILR${stamp.toString().slice(-5)}`.toUpperCase(),
      description: 'item live-reload e2e',
    });

    const originalTitle = `Original title ${stamp}`;
    const updatedTitle = `Updated by agent ${stamp}`;
    const item = await createItemViaAPI(request, ws.id, { title: originalTitle });

    await page.route('**/api/shell-bootstrap', async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      await route.fulfill({
        response,
        json: {
          ...body,
          ai: {
            available: true,
            chat_enabled: true,
            features: { ai_chat: { enabled: true, mode: 'default' } },
          },
        },
      });
    });
    await page.route('**/api/llm/connections', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{ id: 1, name: 'stub', model: 'stub-model', is_default: true }]),
      });
    });
    await page.route('**/api/ai/chat', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          answer: 'ok',
          tool_calls: [],
          iterations: 1,
          max_iterations: 5,
          stop_reason: 'end_turn',
        }),
      });
    });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    await expect(page.getByTestId('item-title-edit')).toContainText(originalTitle);

    // Mutate server-side via the same write path the update_item tool uses.
    await updateItemViaAPI(request, item.id, { title: updatedTitle });

    // Detail still shows the cached title — refetch is gated on agentRuns,
    // not on the 30s poll.
    await expect(page.getByTestId('item-title-edit')).toContainText(originalTitle);

    await page.locator('#chat-toggle-button').click();
    await page.getByTestId('chat-input').fill('Refresh the item');
    await page.getByTestId('chat-send').click();

    await expect(page.getByTestId('item-title-edit')).toContainText(updatedTitle, {
      timeout: 5000,
    });
  });
});
