import {
  chooseSelectOption,
  createActionViaAPI,
  getActionViaAPI,
  nodeConfigByType,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const headers = { 'Sec-Fetch-Site': 'same-origin' };

async function statusCategories(request: APIRequestContext) {
  const resp = await request.get(`${BASE_URL}/api/status-categories`, { headers });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  return (body.data ?? body) as Array<{ id: number; name: string }>;
}

// P1-6: transition_item category-name mode uses a category picker (friendly
// names) instead of raw free text.
test.describe('Action editor — transition_item category picker', () => {
  test('choosing a status category by name persists target.category_name', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `transition-cat-${stamp}`,
      key: `TC${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'transition category e2e',
    });
    const categories = await statusCategories(request);
    expect(categories.length).toBeGreaterThan(0);
    const chosen = categories.find((c) => /done|complete/i.test(c.name)) ?? categories[0];

    const action = await createActionViaAPI(request, ws.id, {
      name: `transition-cat-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'transition_item',
          node_config: JSON.stringify({
            target: { mode: 'category_name' },
            skip_if_already_matching: true,
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    });
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'transition_item');
    await chooseSelectOption(page, 'config-transition-category', chosen.name);
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'transition_item') as {
      target: { mode: string; category_name: string };
    };
    expect(cfg.target.mode).toBe('category_name');
    expect(cfg.target.category_name).toBe(chosen.name);
  });
});
