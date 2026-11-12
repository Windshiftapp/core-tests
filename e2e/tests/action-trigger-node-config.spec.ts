import { test, expect } from '../fixtures/errors';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Action editor trigger/node config contracts', () => {
  test('saving an applied template in the UI preserves action trigger_config', async ({
    page,
    request,
  }) => {

    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `template-trigger-preserve-${stamp}`,
      key: `TTP${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'E2E workspace for template trigger preservation',
    });

    const applyResp = await request.post(
      `${BASE_URL}/api/v2/workspaces/${ws.id}/action-templates/close_subtasks_on_parent_close/apply`,
      { headers: defaultHeaders },
    );
    expect(applyResp.status(), `apply template failed: ${await applyResp.text()}`).toBe(201);
    const applied = (await applyResp.json()).data;

    await page.goto(`/workspaces/${ws.id}/actions/${applied.action_id}`);
    await expect(page.locator('.svelte-flow')).toBeVisible();
    await page.getByTestId('action-editor-save').click();

    const detailResp = await request.get(
      `${BASE_URL}/api/v2/workspaces/${ws.id}/actions/${applied.action_id}`,
      { headers: defaultHeaders },
    );
    expect(detailResp.ok()).toBeTruthy();
    const action = (await detailResp.json()).data;
    expect(JSON.parse(action.trigger_config)).toEqual({ to_status_category_completed: true });
  });
});
