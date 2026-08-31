import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import type { Page } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';

/**
 * Live-reload contract: after the AI chat agent finishes a run, the action
 * editor must refetch and rehydrate to reflect any server-side changes the
 * agent's tool calls produced, without waiting for a poll.
 *
 * Driving this end-to-end through a real LLM would be flaky and expensive.
 * The frontend pipeline is:
 *
 *   chat response → chatStore.sendMessage success →
 *     agentRuns.emit → ActionFlowEditor subscriber → refetch + init
 *
 * The interesting piece for the user — that the editor reacts to a "the
 * agent just finished" signal — sits *below* the chatStore pipe. So we:
 *
 *   1. Apply a template via the existing endpoint to seed a known
 *      3-node / 2-edge action and open it in the editor.
 *   2. Persist a 1-node replacement via REST PUT (same write path the
 *      backend update_action tool uses).
 *   3. Open the chat panel, type a message, and submit it through the UI.
 *   4. Assert the editor refetches and shows 1 node / 0 edges.
 *
 * The pure chatStore-side (emit after success) is small enough to verify by
 * reading; if it grows, a focused vitest is the right place.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

async function openEditorForAction(page: Page, workspaceId: number, actionId: number) {
  await page.goto(`/workspaces/${workspaceId}/actions`);
  const row = page.getByTestId(`action-card-${actionId}`);
  await expect(row).toBeVisible();
  await row.getByTestId(`action-edit-${actionId}`).click();
  await expect(page.getByTestId('action-editor-canvas')).toBeVisible();
}

test.describe('Chat live-reload of agent-driven action edits', () => {
  test('editor refetches when agentRuns signals an agent completion', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);

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

    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `chat-live-reload-${stamp}`,
      key: `CLR${stamp.toString().slice(-6)}`.toUpperCase(),
      description: 'live-reload e2e',
    });

    const applyResp = await request.post(
      `${BASE_URL}/api/workspaces/${ws.id}/action-templates/close_subtasks_on_parent_close/apply`,
      { headers: defaultHeaders }
    );
    expect(applyResp.ok(), `apply failed: ${applyResp.status()}`).toBeTruthy();
    const apply = await applyResp.json();
    const actionId: number = apply.action_id;
    expect(actionId).toBeGreaterThan(0);

    await openEditorForAction(page, ws.id, actionId);
    await expect(page.getByTestId(/^action-node-/)).toHaveCount(3);

    // Replace the graph server-side. Same write path the chat-driven
    // update_action tool uses (ActionRepository.SaveActionWithNodesAndEdges).
    const putResp = await request.put(`${BASE_URL}/api/workspaces/${ws.id}/actions/${actionId}`, {
      headers: defaultHeaders,
      data: {
        name: apply.name,
        description: '',
        is_enabled: true,
        trigger_type: 'status_transition',
        trigger_config: '{"to_status_category_completed":true}',
        nodes: [{ id: 1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 }],
        edges: [],
      },
    });
    expect(putResp.ok(), `put failed: ${putResp.status()} ${await putResp.text()}`).toBeTruthy();

    // Editor still showing the cached 3-node graph — refetch is gated on
    // the agentRuns signal, not on a poll.
    await expect(page.getByTestId(/^action-node-/)).toHaveCount(3);

    await page.locator('#chat-toggle-button').click();
    await page.getByTestId('chat-input').fill('Refresh the action');
    await page.getByTestId('chat-send').click();

    await expect(page.getByTestId(/^action-node-/)).toHaveCount(1, {
      timeout: 5000,
    });
    await expect(page.getByTestId('action-node-trigger')).toBeVisible();
  });
});
