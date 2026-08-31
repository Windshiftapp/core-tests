import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/role-context';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function createAssetAction(
  request: APIRequestContext,
  setId: number,
  name: string,
  workspaceId: number,
  itemTypeId: number,
  itemTitle: string
): Promise<{ id: number }> {
  const response = await request.post(`/api/asset-sets/${setId}/actions`, {
    headers: SEC_FETCH,
    data: {
      name,
      trigger_type: 'asset_created',
      nodes: [
        {
          id: 1,
          node_type: 'trigger',
          node_config: '{}',
          position_x: 0,
          position_y: 0,
        },
        {
          id: 2,
          node_type: 'create_item',
          node_config: JSON.stringify({
            workspace_id: workspaceId,
            item_type_id: itemTypeId,
            title: itemTitle,
          }),
          position_x: 200,
          position_y: 0,
        },
      ],
      edges: [
        {
          source_node_id: 1,
          target_node_id: 2,
          edge_type: 'default',
        },
      ],
    },
  });
  expect(response.status(), `create ${name}: ${await response.text()}`).toBe(201);
  return response.json();
}

test.describe('Asset create-item action workspace permissions (WI-652)', () => {
  test('browser-created asset denies a foreign target and creates in an allowed workspace', async ({
    browser,
    getCtx,
  }) => {
    const admin = await getCtx('admin');
    const member = await getCtx('member');
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const deniedItemTitle = `e2e asset denied ${stamp}`;
    const allowedItemTitle = `e2e asset allowed ${stamp}`;

    // API calls below create prerequisites only. The asset creation that fires
    // both actions, the allowed-item assertion, and the denied-log assertion
    // all run through the browser UI.
    const setResponse = await admin.request.post('/api/asset-sets', {
      headers: SEC_FETCH,
      data: {
        name: `e2e-asset-action-permissions-${stamp}`,
        description: 'WI-652 browser regression',
        is_default: true,
      },
    });
    expect(setResponse.status(), await setResponse.text()).toBe(201);
    const set = (await setResponse.json()) as { id: number };

    const typeResponse = await admin.request.post(`/api/asset-sets/${set.id}/types`, {
      headers: SEC_FETCH,
      data: { name: `Server ${stamp}` },
    });
    expect(typeResponse.status(), await typeResponse.text()).toBe(201);

    const rolesResponse = await admin.request.get('/api/asset-roles', { headers: SEC_FETCH });
    expect(rolesResponse.ok()).toBeTruthy();
    const roles = (await rolesResponse.json()) as Array<{ id: number; name: string }>;
    const assetAdminRole = roles.find((role) => role.name === 'Administrator');
    if (!assetAdminRole) throw new Error('Administrator asset role not found');
    const assignmentResponse = await admin.request.post(`/api/asset-sets/${set.id}/roles`, {
      headers: SEC_FETCH,
      data: { user_id: member.userId, role_id: assetAdminRole.id },
    });
    expect(assignmentResponse.status(), await assignmentResponse.text()).toBe(201);

    const itemTypesResponse = await admin.request.get(
      `/api/item-types?workspace_id=${member.workspaceId}`,
      { headers: SEC_FETCH }
    );
    expect(itemTypesResponse.ok()).toBeTruthy();
    const itemTypesBody = await itemTypesResponse.json();
    const itemTypes = (itemTypesBody.data ?? itemTypesBody) as Array<{ id: number }>;
    expect(itemTypes.length).toBeGreaterThan(0);

    // Fresh workspaces intentionally leave Editor open to every authenticated
    // user until the role has an explicit assignment. Restrict the foreign
    // target by assigning Editor to the admin only; the member actor then has
    // Viewer fallback but no item.create permission there.
    const workspaceRolesResponse = await admin.request.get('/api/workspace-roles', {
      headers: SEC_FETCH,
    });
    expect(workspaceRolesResponse.ok()).toBeTruthy();
    const workspaceRolesBody = await workspaceRolesResponse.json();
    const workspaceRoles = (workspaceRolesBody.data ?? workspaceRolesBody) as Array<{
      id: number;
      name: string;
    }>;
    const editorRole = workspaceRoles.find((role) => role.name === 'Editor');
    if (!editorRole) throw new Error('Editor workspace role not found');
    const restrictTargetResponse = await admin.request.post('/api/workspace-roles/assign', {
      headers: SEC_FETCH,
      data: {
        user_id: admin.userId,
        workspace_id: admin.workspaceId,
        role_id: editorRole.id,
      },
    });
    expect(restrictTargetResponse.ok(), await restrictTargetResponse.text()).toBeTruthy();

    const deniedAction = await createAssetAction(
      admin.request,
      set.id,
      `Denied target ${stamp}`,
      admin.workspaceId,
      itemTypes[0].id,
      deniedItemTitle
    );
    await createAssetAction(
      admin.request,
      set.id,
      `Allowed target ${stamp}`,
      member.workspaceId,
      itemTypes[0].id,
      allowedItemTitle
    );

    const browserContext = await browser.newContext({
      baseURL: BASE_URL,
      storageState: await member.request.storageState(),
    });
    const page = await browserContext.newPage();
    try {
      await page.goto('/assets');
      await page.getByTestId('asset-create').click();
      await page.locator('#asset-title-input').fill(`Browser-created asset ${stamp}`);
      await page.getByTestId('asset-submit').click();
      await expect(page.locator('#asset-title-input')).toBeHidden({ timeout: 10_000 });

      await page.goto(`/workspaces/${member.workspaceId}/backlog`);
      await expect
        .poll(
          async () => {
            await page.reload({ waitUntil: 'networkidle' });
            const rows = await page.getByTestId('backlog-item').allTextContents();
            return rows.some((row) => row.includes(allowedItemTitle));
          },
          { timeout: 20_000 }
        )
        .toBe(true);

      await page.goto('/assets/settings');
      await page.getByTestId('asset-automations-tab').click();
      const deniedCard = page.getByTestId(`action-card-${deniedAction.id}`);
      await deniedCard.getByTestId('action-view-logs').click();
      await expect
        .poll(async () => (await page.getByTestId('action-log-row').allTextContents()).join('\n'), {
          timeout: 15_000,
        })
        .toContain('not authorized (item.create)');
    } finally {
      await browserContext.close();
    }
  });
});
