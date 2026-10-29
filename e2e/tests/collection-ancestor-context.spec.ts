import { randomUUID } from 'node:crypto';
import {
  createItemViaAPI,
  createWorkspaceViaAPI,
  listItemTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

// GitHub #271: with "Hide completed" enabled, a completed parent with an
// active child must stay visible as hierarchy context — in the Tree view with
// its real status (not "No status"), and in the Roadmap instead of leaving
// the child as a stray root.
test('keeps a completed parent visible as context when hide completed filters it', async ({
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
      name: `Shipped ${stamp}`,
      color: '#008800',
      is_completed: true,
    });
    const active = await create('/api/v2/statuses', {
      name: `In progress ${stamp}`,
      category_id: activeCategory.id,
    });
    const completed = await create('/api/v2/statuses', {
      name: `Released ${stamp}`,
      category_id: completedCategory.id,
    });
    const workflow = await create('/api/v2/workflows', { name: `Context ${stamp}` });
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
      name: `Context ${stamp}`,
      key: `CX${stamp}`.toUpperCase(),
      description: 'Completed parent context regression',
    });
    cleanup.unshift(`/api/v2/workspaces/${workspace.id}`);
    const itemTypes = await listItemTypesViaAPI(request);
    // Initiative sits at hierarchy level 0 and stops ancestor walks by design,
    // so use an Epic parent with a Story child (a valid depth-2 hierarchy).
    const epicType = itemTypes.find((type: { builtin_key?: string }) => type.builtin_key === 'epic');
    const storyType = itemTypes.find((type: { builtin_key?: string }) => type.builtin_key === 'story');
    await create(
      '/api/configuration-sets',
      {
        name: `Context ${stamp}`,
        workflow_id: workflow.id,
        workspace_ids: [workspace.id],
        item_type_configs: itemTypes.map((type: { id: number }) => ({ item_type_id: type.id })),
      },
      false
    );
    async function createItem(data: object) {
      const response = await request.post('/api/v2/items', {
        headers,
        data: { ...data, workspace_id: workspace.id },
      });
      expect(response.ok(), await response.text()).toBeTruthy();
      const body = await response.json();
      cleanup.unshift(`/api/v2/items/${body.data.id}`);
      return body.data;
    }
    const done = await createItem({
      title: `Finished epic ${stamp}`,
      item_type_id: epicType.id,
      status_id: completed.id,
    });
    const open = await createItem({
      title: `Follow-up bug ${stamp}`,
      item_type_id: storyType.id,
      parent_id: done.id,
      status_id: active.id,
    });

    const base = `/workspaces/${workspace.id}`;

    await page.goto(`${base}/tree`);
    const toggle = page.getByTestId('collection-hide-completed');
    await expect(toggle).toHaveAttribute('aria-checked', 'true');
    // The completed epic is filtered from the page but still renders as the
    // child's context parent — with its real status, not "No status".
    const epicRow = page.getByTestId(`tree-row-${done.id}`);
    await expect(epicRow).toContainText(`Finished epic ${stamp}`);
    // The context epic carries its real Done status, not "No status".
    await expect(epicRow).toContainText(`Released ${stamp}`);
    await expect(epicRow).not.toContainText('No status');
    await expect(page.getByTestId(`tree-row-${open.id}`)).toContainText(`Follow-up bug ${stamp}`);

    await page.goto(`${base}/roadmap`);
    await expect(page.getByTestId('roadmap-view')).toBeVisible();
    // The epic must not vanish on the roadmap either; its active child is
    // shown beneath it instead of floating as a stray root.
    await expect(page.getByTestId(`roadmap-item-${done.id}`)).toContainText(`Finished epic ${stamp}`);
    await expect(page.getByTestId(`roadmap-item-${open.id}`)).toContainText(`Follow-up bug ${stamp}`);
  } finally {
    for (const path of cleanup) {
      const response = await request.delete(path, { headers });
      expect(response.ok(), `cleanup ${path}: ${await response.text()}`).toBeTruthy();
    }
  }
});
