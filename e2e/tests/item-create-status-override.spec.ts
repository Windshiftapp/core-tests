import { randomUUID } from 'node:crypto';
import { createWorkspaceViaAPI, listItemTypesViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';

/**
 * Create-time status placement (fix(api) d7f8b352 + fix(frontend) d0597772).
 *
 * Contract for POST /api/items:
 *   - status_id omitted → the item lands on the workflow initial status.
 *   - status_id = initial status, or a status directly reachable from the
 *     initial status (board column quick-add) → accepted.
 *   - status_id not reachable from the initial status → 400 validation
 *     error, item is NOT created.
 *   - a reachable status whose transition is gated by workflow conditions/
 *     validators → 400 (create must not bypass the gate).
 *
 * Fixture: fresh workspace bound (via a dedicated configuration_set) to a
 * workflow  Triage(initial) → Ready → Blocked , so Blocked is a real status
 * of the workflow but not reachable in one hop from Triage.
 *
 * The UI test covers the frontend half of the fix: the create modal no
 * longer hardcodes a default status, so a UI-created item must land on the
 * workspace workflow's initial status (Triage), not a global default.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface Fixture {
  workspaceId: number;
  configSetId: number;
  triageId: number; // initial
}

async function fetchDefaultCategoryID(request: APIRequestContext): Promise<number> {
  const resp = await request.get('/api/status-categories', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const cats: Array<{ id: number; is_default: boolean }> = await resp.json();
  const def = cats.find((c) => c.is_default) ?? cats[0];
  expect(def, 'no status categories seeded').toBeTruthy();
  return def.id;
}

async function createStatus(
  request: APIRequestContext,
  name: string,
  categoryID: number
): Promise<number> {
  const resp = await request.post('/api/statuses', {
    headers: SEC_FETCH,
    data: { name, category_id: categoryID },
  });
  expect(resp.ok(), `status ${name} create: ${resp.status()}`).toBeTruthy();
  return (await resp.json()).id as number;
}

async function buildFixture(request: APIRequestContext, prefix: string): Promise<Fixture> {
  const categoryID = await fetchDefaultCategoryID(request);
  const triageId = await createStatus(request, `${prefix}-Triage`, categoryID);
  const readyId = await createStatus(request, `${prefix}-Ready`, categoryID);
  const blockedId = await createStatus(request, `${prefix}-Blocked`, categoryID);

  const wfResp = await request.post('/api/workflows', {
    headers: SEC_FETCH,
    data: { name: `${prefix}-wf`, description: 'e2e create-status-override' },
  });
  expect(wfResp.ok(), `workflow create: ${wfResp.status()}`).toBeTruthy();
  const workflowId = (await wfResp.json()).id as number;

  const txResp = await request.put(`/api/workflows/${workflowId}/transitions`, {
    headers: SEC_FETCH,
    data: [
      { from_status_id: null, to_status_id: triageId }, // initial
      { from_status_id: triageId, to_status_id: readyId },
      { from_status_id: readyId, to_status_id: blockedId },
    ],
  });
  expect(txResp.ok(), `set transitions: ${txResp.status()} ${await txResp.text()}`).toBeTruthy();
  await txResp.json();

  const ws = await createWorkspaceViaAPI(request, generateWorkspace(prefix));

  // A workspace bound to a config set only allows item types listed on the
  // set (IsItemTypeAllowedInWorkspace) — whitelist all global types so the
  // create modal's default type keeps working.
  const itemTypes: Array<{ id: number }> = await listItemTypesViaAPI(request);
  const itemTypeConfigs = itemTypes.map((t) => ({ item_type_id: t.id }));

  // Bind the workspace to the workflow via a dedicated configuration set —
  // workspace_ids creates the workspace_configuration_sets row.
  const csResp = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name: `${prefix}-cs`,
      description: 'e2e create-status-override',
      workflow_id: workflowId,
      workspace_ids: [ws.id],
      item_type_configs: itemTypeConfigs,
    },
  });
  expect(csResp.ok(), `config set create: ${csResp.status()} ${await csResp.text()}`).toBeTruthy();
  const configSetId = (await csResp.json()).id as number;

  return {
    workspaceId: ws.id,
    configSetId,
    triageId,
  };
}

test.describe('Item creation — status placement', () => {
  let fx: Fixture;
  let prefix: string;

  test.beforeAll(async ({ request }, testInfo) => {
    // fullyParallel runs beforeAll once in every worker. Build the prefix in
    // that worker instead of once while Playwright loads the test module.
    prefix = `e2e-cso-${Date.now()}-${testInfo.workerIndex}-${randomUUID().slice(0, 8)}`;
    fx = await buildFixture(request, prefix);
  });

  test.afterAll(async ({ request }) => {
    // The workspace_configuration_sets binding cascade-deletes with the set;
    // statuses/workflow are inert without it and left behind like other specs do.
    if (fx?.configSetId) {
      await request.delete(`/api/configuration-sets/${fx.configSetId}`, { headers: SEC_FETCH });
    }
  });

  test('UI create modal lands the item on the workflow initial status', async ({
    page,
    request,
  }) => {
    // Frontend half of the fix: the create form no longer hardcodes a
    // default status, so the backend's initial-status placement wins.
    const itemPage = new ItemPage(page);
    const title = `${prefix} ui-initial-status`;

    await itemPage.createItem(String(fx.workspaceId), { title });

    // Resolve the created item via the API and assert its status server-side.
    await expect
      .poll(
        async () => {
          const listResp = await request.get(`/api/items?workspace_id=${fx.workspaceId}`, {
            headers: SEC_FETCH,
          });
          if (!listResp.ok()) return null;
          const body = await listResp.json();
          const items: Array<{ title: string; status_id: number | null }> =
            body.data ?? body.items ?? body;
          return items.find((i) => i.title === title)?.status_id ?? null;
        },
        { message: 'UI-created item should appear with the workflow initial status' }
      )
      .toBe(fx.triageId);
  });
});
