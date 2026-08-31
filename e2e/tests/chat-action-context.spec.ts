import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

/**
 * Context-aware chat contract: when the user is editing an action, the
 * ChatPanel must include `context.action_id` in its POST /api/ai/chat body
 * so the backend can append the editor-specific system-prompt hint.
 *
 * The bug (WI-39): the editor used to keep the action id in an in-memory
 * store, which left the chat unable to recover the id after navigation or
 * refresh. The fix puts the id in the URL so route params are the single
 * source of truth.
 *
 * We assert the contract at the network boundary — intercept the chat POST,
 * inspect the body, and stub the response so we don't depend on an LLM.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Chat picks up the action editor context from the URL', () => {
  test('sends action_id when chatting from /workspaces/:id/actions/:actionId', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);

    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `chat-ctx-${stamp}`,
      key: `CCX${stamp.toString().slice(-6)}`.toUpperCase(),
      description: 'chat context e2e',
    });

    const applyResp = await request.post(
      `${BASE_URL}/api/workspaces/${ws.id}/action-templates/close_subtasks_on_parent_close/apply`,
      { headers: defaultHeaders }
    );
    expect(applyResp.ok(), `apply failed: ${applyResp.status()}`).toBeTruthy();
    const apply = await applyResp.json();
    const actionId: number = apply.action_id;
    expect(actionId).toBeGreaterThan(0);

    // The e2e environment doesn't have a real LLM connection, so the AI
    // shell bootstrap reports AI unavailable and the chat toggle never renders.
    // Patch its AI snapshot + the connections list so the panel mounts; capture the
    // chat POST and short-circuit the response so we don't depend on an LLM.
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
        body: JSON.stringify([
          {
            id: 1,
            name: 'OpenRouter: qwen/qwen3-coder-next',
            model: 'stub-model',
            is_default: true,
          },
          {
            id: 2,
            name: 'OpenRouter: deepseek/deepseek-v3.2',
            model: 'stub-model',
            is_default: false,
          },
        ]),
      });
    });
    const chatRequestBody = new Promise<Record<string, unknown>>((resolve) => {
      page.route('**/api/ai/chat', async (route) => {
        const body = route.request().postDataJSON() as Record<string, unknown>;
        resolve(body);
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
    });

    // Open the editor directly via URL. The actionId in the path is what
    // ChatPanel.buildContext reads, so this exercises the bug fix end-to-end.
    await page.goto(`/workspaces/${ws.id}/actions/${actionId}`);
    await expect(page.locator('.svelte-flow')).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${ws.id}/actions/${actionId}$`));

    // Open the chat panel via the global toggle and send a message.
    await page.locator('#chat-toggle-button').click();
    const textarea = page.locator('textarea[placeholder="Ask a question..."]');
    await expect(textarea).toBeVisible();

    const modelSelector = page.locator('#agent-chat-model');
    await expect(modelSelector).toHaveText('Default');
    const selectorBounds = await modelSelector.boundingBox();
    expect(selectorBounds?.width).toBeGreaterThanOrEqual(200);
    expect(selectorBounds?.width).toBeLessThanOrEqual(352);
    await modelSelector.click();
    const longModel = page
      .getByTestId('agent-chat-model-option')
      .filter({ hasText: 'OpenRouter: deepseek/deepseek-v3.2' });
    await expect(longModel).toBeVisible();
    expect(
      await longModel
        .locator('span')
        .evaluate((element) => element.scrollWidth <= element.clientWidth)
    ).toBeTruthy();

    await textarea.fill('hi');
    await page.getByTestId('chat-send').click();

    const body = await chatRequestBody;
    expect(body).toHaveProperty('context');
    const ctx = body.context as Record<string, unknown>;
    expect(ctx.view).toBe('workspace-actions');
    expect(ctx.workspace_id).toBe(ws.id);
    expect(ctx.action_id).toBe(actionId);
  });
});
