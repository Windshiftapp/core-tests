import { type APIRequestContext, expect, type Page } from './context-path';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

export interface ActionNodeInput {
  id: number;
  node_type: string;
  node_config: string;
  position_x: number;
  position_y: number;
}

export interface ActionEdgeInput {
  source_node_id: number;
  target_node_id: number;
  edge_type: string;
}

/**
 * Create an action via the API. Node ids are caller-assigned negatives; the
 * server returns them remapped to real DB ids (used to target canvas nodes by
 * their `node-<id>` data-id in the editor).
 */
export async function createActionViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  data: {
    name: string;
    description?: string;
    trigger_type: string;
    trigger_config?: string;
    nodes: ActionNodeInput[];
    edges?: ActionEdgeInput[];
  }
) {
  const resp = await request.post(`${BASE_URL}/api/workspaces/${workspaceId}/actions`, {
    headers: defaultHeaders,
    data: { trigger_config: '{}', edges: [], ...data },
  });
  expect(resp.status(), `create action failed: ${await resp.text()}`).toBe(201);
  return resp.json();
}

export async function getActionViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  actionId: number
) {
  const resp = await request.get(`${BASE_URL}/api/workspaces/${workspaceId}/actions/${actionId}`, {
    headers: defaultHeaders,
  });
  expect(resp.ok(), `get action failed: ${resp.status()}`).toBeTruthy();
  return resp.json();
}

export async function openActionEditor(page: Page, workspaceId: number, actionId: number) {
  await page.goto(`/workspaces/${workspaceId}/actions/${actionId}`);
  await expect(page.getByTestId('action-editor-canvas')).toBeVisible();
  // The editor re-fetches and re-inits the flow store once on mount (live
  // reload on agent runs). Let that settle before interacting, otherwise an
  // early edit can be clobbered by the re-init.
  await page.waitForLoadState('networkidle');
}

/**
 * Click a canvas node by its type. The node component exposes a stable test id
 * so this follows the same pointer path as a user and does not depend on
 * xyflow's generated class names.
 */
export async function selectNodeByType(page: Page, nodeType: string) {
  const node = page.getByTestId(`action-node-${nodeType}`).first();
  await expect(node).toBeVisible();
  await node.click();
}

/**
 * Drive a shared `Select` component: click the trigger button by id, then click
 * the option whose value matches (options carry `data-option-id`). Only the
 * open select renders its listbox, so the option selector is unambiguous.
 */
export async function chooseSelectOption(
  page: Page,
  triggerId: string,
  optionValue: string | number
) {
  await page.locator(`#${triggerId}`).click();
  await page.locator(`[data-option-id="${optionValue}"]`).click();
}

/**
 * Save the action through the real button interaction and wait for the PUT to
 * resolve. If the button is covered or disabled, the test should expose that
 * user-visible defect rather than bypassing it.
 */
export async function saveAction(page: Page, workspaceId: number, actionId: number) {
  const [resp] = await Promise.all([
    page.waitForResponse(
      (r) =>
        r.url().includes(`/api/workspaces/${workspaceId}/actions/${actionId}`) &&
        r.request().method() === 'PUT'
    ),
    page.getByTestId('action-editor-save').click(),
  ]);
  expect(resp.ok(), `save failed: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return resp;
}

/**
 * Parse a node's serialized node_config from a fetched action by node type.
 * Node ids are reassigned by the server on save, so look up by type instead.
 */
export function nodeConfigByType(
  action: { nodes: Array<{ node_type: string; node_config: string }> },
  nodeType: string
): Record<string, unknown> {
  const node = action.nodes.find((n) => n.node_type === nodeType);
  if (!node) throw new Error(`node of type ${nodeType} not found in action`);
  return JSON.parse(node.node_config || '{}');
}
