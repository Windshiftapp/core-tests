import {
  chooseSelectOption,
  createActionViaAPI,
  getActionViaAPI,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createWorkspaceViaAPI, listLinkTypesViaAPI } from '../fixtures/api-helpers';
import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const headers = { 'Sec-Fetch-Site': 'same-origin' };

async function workspaceItemTypes(request: APIRequestContext, wsId: number) {
  const resp = await request.get(`${BASE_URL}/api/item-types?workspace_id=${wsId}`, { headers });
  expect(resp.ok(), `item types fetch failed: ${resp.status()}`).toBeTruthy();
  const body = await resp.json();
  return (body.data ?? body) as Array<{ id: number; name: string }>;
}

async function workspaceStatuses(request: APIRequestContext, wsId: number) {
  const resp = await request.get(`${BASE_URL}/api/workspaces/${wsId}/statuses`, { headers });
  expect(resp.ok()).toBeTruthy();
  return (await resp.json()) as Array<{ id: number; name: string }>;
}

function triggerOnlyAction(stamp: number, triggerType: string, triggerConfig: object = {}) {
  return {
    name: `trigger-filter-${triggerType}-${stamp}`,
    trigger_type: triggerType,
    trigger_config: JSON.stringify(triggerConfig),
    nodes: [
      {
        id: -1,
        node_type: 'trigger',
        node_config: JSON.stringify(triggerConfig),
        position_x: 0,
        position_y: 0,
      },
    ],
    edges: [],
  };
}

// P0-2: trigger filters for item type, link type, and the completed-category
// terminal toggle are configurable and persist.
test.describe('Action editor — trigger filters', () => {
  test('item_created exposes an item-type control that serializes item_type_id', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `trig-itemtype-${stamp}`,
      key: `TIT${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'trigger item type e2e',
    });
    const itemTypes = await workspaceItemTypes(request, ws.id);
    expect(itemTypes.length, 'workspace has item types').toBeGreaterThan(0);
    const chosen = itemTypes[0];

    const action = await createActionViaAPI(
      request,
      ws.id,
      triggerOnlyAction(stamp, 'item_created')
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'trigger');
    await chooseSelectOption(page, 'config-item-type', chosen.id);
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(JSON.parse(fresh.trigger_config).item_type_id).toBe(chosen.id);

    // Hydration: reopening shows the friendly type name, not the raw id.
    await openActionEditor(page, ws.id, fresh.id);
    await selectNodeByType(page, 'trigger');
    await expect(page.locator('#config-item-type')).toContainText(chosen.name);
  });

  test('item_linked exposes a link-type control that serializes link_type_id', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `trig-linktype-${stamp}`,
      key: `TLT${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'trigger link type e2e',
    });
    const linkTypes = await listLinkTypesViaAPI(request);
    const chosen = linkTypes.find((lt) => lt.name === 'Relates To') ?? linkTypes[0];

    const action = await createActionViaAPI(
      request,
      ws.id,
      triggerOnlyAction(stamp, 'item_linked')
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'trigger');
    await chooseSelectOption(page, 'config-trigger-link-type', chosen.id);
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(JSON.parse(fresh.trigger_config).link_type_id).toBe(chosen.id);
  });

  test('status_transition completed toggle serializes to_status_category_completed and clears to_status_id', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `trig-completed-${stamp}`,
      key: `TCM${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'trigger completed toggle e2e',
    });
    const statuses = await workspaceStatuses(request, ws.id);
    const seed = statuses[0];

    const action = await createActionViaAPI(
      request,
      ws.id,
      triggerOnlyAction(stamp, 'status_transition', { to_status_id: seed.id })
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'trigger');
    await page.getByTestId('trigger-completed-toggle').click();
    // Toggling "any completed status" disables the explicit To Status picker.
    await expect(page.locator('#config-to-status')).toBeDisabled();

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = JSON.parse(fresh.trigger_config);
    expect(cfg.to_status_category_completed).toBe(true);
    expect(cfg.to_status_id ?? null).toBeNull();
  });
});
