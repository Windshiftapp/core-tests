import { test, expect } from '../fixtures/errors';
import type { APIRequestContext } from '../fixtures/context-path';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import {
  createActionViaAPI,
  getActionViaAPI,
  openActionEditor,
  selectNodeByType,
  saveAction,
  chooseSelectOption,
  nodeConfigByType,
  type ActionNodeInput,
} from '../fixtures/action-editor-helpers';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const headers = { 'Sec-Fetch-Site': 'same-origin' };

async function createCannedResponseViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  data: { name: string; body: string; is_private?: boolean }
) {
  const resp = await request.post(`${BASE_URL}/api/v2/workspaces/${workspaceId}/canned-responses`, {
    headers,
    data: { is_private: false, ...data },
  });
  expect(resp.status(), `create canned response failed: ${await resp.text()}`).toBe(201);
  return (await resp.json()).data as { id: number; name: string };
}

async function createLabelViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  name: string
) {
  const resp = await request.post(`${BASE_URL}/api/v2/workspaces/${workspaceId}/labels`, {
    headers,
    data: { name },
  });
  expect(resp.status(), `create label failed: ${await resp.text()}`).toBe(201);
  return (await resp.json()).data as { id: number; name: string };
}

function triggerNode(triggerConfig: object = {}): ActionNodeInput {
  return {
    id: -1,
    node_type: 'trigger',
    node_config: JSON.stringify(triggerConfig),
    position_x: 0,
    position_y: 0,
  };
}

function actionWithNode(stamp: number, triggerType: string, node: Omit<ActionNodeInput, 'id'>) {
  return {
    name: `helpdesk-node-${stamp}`,
    trigger_type: triggerType,
    trigger_config: '{}',
    nodes: [triggerNode(), { id: -2, ...node }],
    edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
  };
}

// WI-1132/WI-1138 follow-up: the helpdesk automation nodes and triggers are
// configurable from the action editor, not just the API.
test.describe('Action editor — helpdesk automation nodes and triggers', () => {
  test('palette exposes notify_customer, insert_canned_response, and adjust_labels', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-palette-${stamp}`,
      key: `HDP${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'helpdesk node palette e2e',
    });

    const action = await createActionViaAPI(request, ws.id, {
      name: `helpdesk-palette-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [triggerNode()],
      edges: [],
    });

    await openActionEditor(page, ws.id, action.id);

    await expect(page.getByTestId('action-palette-notify_customer')).toBeVisible();
    await expect(page.getByTestId('action-palette-insert_canned_response')).toBeVisible();
    await expect(page.getByTestId('action-palette-adjust_labels')).toBeVisible();
  });

  test('comment_created trigger persists the from_customer filter', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-comment-${stamp}`,
      key: `HDC${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'comment_created trigger e2e',
    });

    const action = await createActionViaAPI(request, ws.id, {
      name: `helpdesk-comment-${stamp}`,
      trigger_type: 'comment_created',
      trigger_config: '{}',
      nodes: [triggerNode()],
      edges: [],
    });

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'trigger');
    await chooseSelectOption(page, 'config-comment-author', 'customer');
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(JSON.parse(fresh.trigger_config).from_customer).toBe(true);
  });

  test('item_inactive trigger persists inactive_hours', async ({ page, request, allowConsoleError }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-inactive-${stamp}`,
      key: `HDI${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'item_inactive trigger e2e',
    });

    const action = await createActionViaAPI(request, ws.id, {
      name: `helpdesk-inactive-${stamp}`,
      trigger_type: 'item_inactive',
      trigger_config: JSON.stringify({ inactive_hours: 24 }),
      nodes: [triggerNode({ inactive_hours: 24 })],
      edges: [],
    });

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'trigger');
    await page.getByTestId('trigger-inactive-hours').fill('48');
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(JSON.parse(fresh.trigger_config).inactive_hours).toBe(48);
  });

  test('notify_customer subject and message persist', async ({ page, request, allowConsoleError }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-notify-${stamp}`,
      key: `HDN${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'notify_customer node e2e',
    });

    const action = await createActionViaAPI(
      request,
      ws.id,
      actionWithNode(stamp, 'manual', {
        node_type: 'notify_customer',
        node_config: JSON.stringify({ message: 'initial' }),
        position_x: 240,
        position_y: 0,
      })
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'notify_customer');
    await page.getByTestId('notify-customer-subject').fill('We are on it');
    await page.getByTestId('notify-customer-message').fill('Thanks for reaching out, {{requester.name}}.');
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'notify_customer');
    expect(cfg.subject).toBe('We are on it');
    expect(cfg.message).toBe('Thanks for reaching out, {{requester.name}}.');
  });

  test('insert_canned_response picker persists the selected response id', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-canned-${stamp}`,
      key: `HDR${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'insert_canned_response node e2e',
    });
    const canned = await createCannedResponseViaAPI(request, ws.id, {
      name: `greeting-${stamp}`,
      body: 'Hello from support',
    });

    const action = await createActionViaAPI(
      request,
      ws.id,
      actionWithNode(stamp, 'manual', {
        node_type: 'insert_canned_response',
        node_config: JSON.stringify({ canned_response_id: 0 }),
        position_x: 240,
        position_y: 0,
      })
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'insert_canned_response');
    await chooseSelectOption(page, 'config-canned-response', canned.id);
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(nodeConfigByType(fresh, 'insert_canned_response').canned_response_id).toBe(canned.id);
  });

  test('adjust_labels persists add and remove selections', async ({ page, request, allowConsoleError }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `helpdesk-labels-${stamp}`,
      key: `HDL${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'adjust_labels node e2e',
    });
    const add = await createLabelViaAPI(request, ws.id, `vip-${stamp}`);
    const remove = await createLabelViaAPI(request, ws.id, `stale-${stamp}`);

    const action = await createActionViaAPI(
      request,
      ws.id,
      actionWithNode(stamp, 'manual', { node_type: 'adjust_labels', node_config: '{}', position_x: 240, position_y: 0 })
    );

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'adjust_labels');
    await page.getByTestId(`adjust-label-add-${add.id}`).click();
    await page.getByTestId(`adjust-label-remove-${remove.id}`).click();
    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'adjust_labels');
    expect(cfg.add_label_ids).toEqual([add.id]);
    expect(cfg.remove_label_ids).toEqual([remove.id]);
  });
});
