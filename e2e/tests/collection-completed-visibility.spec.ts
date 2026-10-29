import { randomUUID } from 'node:crypto';
import {
  createItemViaAPI,
  createWorkspaceViaAPI,
  listItemTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';
import { preferenceSave } from '../helpers/preferences';

test('hides custom completed categories and remembers visibility separately for list and tree', async ({
  page,
  request,
}) => {
  const stamp = randomUUID().slice(0, 8);
  const headers = { 'Sec-Fetch-Site': 'same-origin' };
  const cleanup: string[] = [];
  async function create(path: string, data: object, v2 = true) {
    const response = await request.post(path, { headers, data });
    expect(response.ok(), await response.text()).toBeTruthy();
    const body = await response.json();
    const value = v2 ? body.data : body;
    cleanup.unshift(`${path}/${value.id}`);
    return value;
  }

  try {
    const activeCategory = await create('/api/v2/status-categories', {
      name: `Active ${stamp}`,
      color: '#0000ff',
      is_completed: false,
    });
    const completedCategory = await create('/api/v2/status-categories', {
      name: `Archived ${stamp}`,
      color: '#008800',
      is_completed: true,
    });
    const active = await create('/api/v2/statuses', {
      name: `Done ${stamp}`,
      category_id: activeCategory.id,
    });
    const completed = await create('/api/v2/statuses', {
      name: `Released ${stamp}`,
      category_id: completedCategory.id,
    });
    const workflow = await create('/api/v2/workflows', { name: `Visibility ${stamp}` });
    const transitions = await request.put(`/api/v2/workflows/${workflow.id}/transitions`, {
      headers,
      data: {
        transitions: [
          { from_status_id: null, to_status_id: active.id },
          { from_status_id: active.id, to_status_id: completed.id },
          { from_status_id: completed.id, to_status_id: active.id },
        ],
      },
    });
    expect(transitions.ok(), await transitions.text()).toBeTruthy();
    const workspace = await createWorkspaceViaAPI(request, {
      name: `Visibility ${stamp}`,
      key: `CV${stamp}`.toUpperCase(),
      description: 'Completion visibility regression',
    });
    cleanup.unshift(`/api/v2/workspaces/${workspace.id}`);
    const itemTypes = await listItemTypesViaAPI(request);
    await create(
      '/api/configuration-sets',
      {
        name: `Visibility ${stamp}`,
        workflow_id: workflow.id,
        workspace_ids: [workspace.id],
        item_type_configs: itemTypes.map((type: { id: number }) => ({ item_type_id: type.id })),
      },
      false
    );
    const done = await createItemViaAPI(request, workspace.id, {
      title: `Released parent ${stamp}`,
      status_id: completed.id,
    });
    const open = await createItemViaAPI(request, workspace.id, {
      title: `Active child ${stamp}`,
      parent_id: done.id,
    });

    const base = `/workspaces/${workspace.id}`;
    await page.goto(`${base}/list`);
    const toggle = page.getByTestId('collection-hide-completed');
    await expect(toggle).toHaveAttribute('aria-label', 'Hide completed');
    await expect(toggle).toHaveAttribute('aria-checked', 'true');
    await expect(page.getByTestId(`workspace-item-row-${open.id}`)).toContainText(open.title);
    await expect(page.getByTestId(`workspace-item-row-${done.id}`)).toHaveCount(0);
    const saved = preferenceSave(page);
    await toggle.click();
    await saved;
    await expect(page.getByTestId(`workspace-item-row-${done.id}`)).toContainText(done.title);
    await page.reload();
    await expect(toggle).toHaveAttribute('aria-checked', 'false');
    await expect(page.getByTestId(`workspace-item-row-${done.id}`)).toContainText(done.title);

    await page.goto(`${base}/tree`);
    await expect(toggle).toHaveAttribute('aria-checked', 'true');
    await expect(page.getByTestId(`tree-row-${open.id}`)).toContainText(open.title);
    await expect(page.getByTestId(`tree-row-${done.id}`)).toHaveCount(0);
    await toggle.click();
    await expect(page.getByTestId(`tree-row-${done.id}`)).toContainText(done.title);
    await toggle.click();
    await expect(page.getByTestId(`tree-row-${done.id}`)).toHaveCount(0);
    await expect(page.getByTestId(`tree-row-${open.id}`)).toContainText(open.title);
    await page.goto(`${base}/list`);
    await expect(toggle).toHaveAttribute('aria-checked', 'false');
    await expect(page.getByTestId(`workspace-item-row-${done.id}`)).toContainText(done.title);
  } finally {
    for (const path of cleanup) {
      const response = await request.delete(path, { headers });
      expect(response.ok(), `cleanup ${path}: ${await response.text()}`).toBeTruthy();
    }
  }
});
