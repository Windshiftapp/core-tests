import type { APIRequestContext } from '@playwright/test';
import {
  createCustomFieldViaAPI,
  createItemViaAPI,
  createUserViaAPI,
  createWorkspaceViaAPI,
  deleteCustomFieldViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function workspaceRoleID(request: APIRequestContext, name: string): Promise<number> {
  const response = await request.get('/api/workspace-roles', {
    headers: SEC_FETCH,
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  const role = (body.data ?? body).find((candidate: { name: string }) => candidate.name === name);
  expect(role, `workspace role "${name}" not found`).toBeDefined();
  return role.id;
}

async function assignWorkspaceRole(
  request: APIRequestContext,
  userID: number,
  workspaceID: number,
  roleID: number
): Promise<void> {
  const response = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userID, workspace_id: workspaceID, role_id: roleID },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function createScreen(request: APIRequestContext, name: string): Promise<{ id: number }> {
  const response = await request.post('/api/screens', {
    headers: SEC_FETCH,
    data: { name, description: 'Multi-user reference rendering E2E screen' },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function setScreenFields(
  request: APIRequestContext,
  screenID: number,
  fieldIDs: number[]
): Promise<void> {
  const response = await request.put(`/api/screens/${screenID}/fields`, {
    headers: SEC_FETCH,
    data: fieldIDs.map((fieldID, displayOrder) => ({
      field_type: 'custom',
      field_identifier: String(fieldID),
      display_order: displayOrder,
      is_required: false,
      field_width: 'full',
    })),
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function createConfigurationSet(
  request: APIRequestContext,
  name: string,
  workspaceID: number,
  screenID: number
): Promise<{ id: number }> {
  const response = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name,
      description: 'Multi-user reference rendering E2E configuration',
      workspace_ids: [workspaceID],
      create_screen_id: screenID,
      edit_screen_id: screenID,
      view_screen_id: screenID,
    },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

test('non-admin reference fields resolve names without leaking inaccessible assets', async ({
  request,
  browser,
}) => {
  test.setTimeout(120_000);

  const stamp = `murp-${Date.now().toString(36)}`;
  const actorData = generateUser(`${stamp}-actor`);
  const referencedData = generateUser(`${stamp}-reference`);
  const actor = await createUserViaAPI(request, actorData);
  const referencedUser = await createUserViaAPI(request, referencedData);
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(stamp));

  const viewerRoleID = await workspaceRoleID(request, 'Viewer');
  const editorRoleID = await workspaceRoleID(request, 'Editor');
  await assignWorkspaceRole(request, actor.id, workspace.id, viewerRoleID);

  // Close the implicit Editor fallback for this workspace. The browser actor
  // must exercise the genuinely read-only, non-admin rendering path.
  const meResponse = await request.get('/api/auth/me', { headers: SEC_FETCH });
  expect(meResponse.ok(), await meResponse.text()).toBeTruthy();
  const me = await meResponse.json();
  await assignWorkspaceRole(request, me.user.id, workspace.id, editorRoleID);

  const assetSetResponse = await request.post('/api/asset-sets', {
    headers: SEC_FETCH,
    data: { name: `Restricted asset set ${stamp}` },
  });
  expect(assetSetResponse.status(), await assetSetResponse.text()).toBe(201);
  const assetSet = (await assetSetResponse.json()) as { id: number };

  const assetTypeResponse = await request.post(`/api/asset-sets/${assetSet.id}/types`, {
    headers: SEC_FETCH,
    data: { name: `Restricted type ${stamp}` },
  });
  expect(assetTypeResponse.status(), await assetTypeResponse.text()).toBe(201);
  const assetType = (await assetTypeResponse.json()) as { id: number };

  const restrictedAssetTitle = `Restricted asset ${stamp}`;
  const restrictedAssetTag = `SECRET-${stamp.toUpperCase()}`;
  const assetResponse = await request.post(`/api/asset-sets/${assetSet.id}/assets`, {
    headers: SEC_FETCH,
    data: {
      asset_type_id: assetType.id,
      title: restrictedAssetTitle,
      description: 'Must only resolve for users with asset.view',
      asset_tag: restrictedAssetTag,
    },
  });
  expect(assetResponse.status(), await assetResponse.text()).toBe(201);
  const asset = (await assetResponse.json()) as { id: number };

  const createdFieldIDs: number[] = [];
  const userField = await createCustomFieldViaAPI(request, {
    name: `Owner ${stamp}`,
    field_type: 'user',
  });
  createdFieldIDs.push(userField.id);
  const multiUserField = await createCustomFieldViaAPI(request, {
    name: `Reviewers ${stamp}`,
    field_type: 'multi_user',
  });
  createdFieldIDs.push(multiUserField.id);
  const assetField = await createCustomFieldViaAPI(request, {
    name: `Device ${stamp}`,
    field_type: 'asset',
    options: JSON.stringify({ asset_set_id: assetSet.id }),
  });
  createdFieldIDs.push(assetField.id);

  const screen = await createScreen(request, `Reference screen ${stamp}`);
  await setScreenFields(request, screen.id, createdFieldIDs);
  const configSet = await createConfigurationSet(
    request,
    `Reference configuration ${stamp}`,
    workspace.id,
    screen.id
  );
  const item = await createItemViaAPI(request, workspace.id, {
    title: `Reference rendering ${stamp}`,
    custom_field_values: {
      [userField.id]: referencedUser.id,
      [multiUserField.id]: [actor.id, referencedUser.id],
      [assetField.id]: asset.id,
    },
  });

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });

  try {
    const loginResponse = await context.request.post('/api/auth/login', {
      headers: SEC_FETCH,
      data: {
        email_or_username: actorData.username,
        password: actorData.password_hash,
        remember_me: false,
      },
    });
    expect(loginResponse.ok(), await loginResponse.text()).toBeTruthy();

    const page = await context.newPage();
    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();

    const referencedName = `${referencedData.first_name} ${referencedData.last_name}`;
    const actorName = `${actorData.first_name} ${actorData.last_name}`;
    await expect(page.getByTestId(`item-custom-field-${userField.id}`)).toContainText(
      referencedName
    );
    await expect(page.getByTestId(`item-custom-field-${multiUserField.id}`)).toContainText(
      actorName
    );
    await expect(page.getByTestId(`item-custom-field-${multiUserField.id}`)).toContainText(
      referencedName
    );
    await expect(page.getByTestId(`item-custom-field-label-${multiUserField.id}`)).toHaveCSS(
      'white-space',
      'nowrap'
    );
    await expect(page.getByTestId(`item-custom-field-display-${multiUserField.id}`)).toHaveCSS(
      'white-space',
      'nowrap'
    );

    // No asset set is visible to this actor, so navigation stays hidden and
    // the stored reference remains recognizable without exposing its title.
    await expect(page.locator('#nav-assets')).toHaveCount(0);
    const assetFieldRow = page.getByTestId(`item-custom-field-${assetField.id}`);
    await expect(assetFieldRow).toContainText(`Asset #${asset.id}`);
    await expect(assetFieldRow).not.toContainText(restrictedAssetTitle);
    await expect(assetFieldRow).not.toContainText(restrictedAssetTag);

    // Granting per-set Viewer permission should make both the asset module and
    // the same stored reference resolve after a fresh browser read.
    const assetRolesResponse = await request.get('/api/asset-roles', {
      headers: SEC_FETCH,
    });
    expect(assetRolesResponse.ok(), await assetRolesResponse.text()).toBeTruthy();
    const assetRoles = (await assetRolesResponse.json()) as Array<{
      id: number;
      name: string;
    }>;
    const assetViewer = assetRoles.find((role) => role.name === 'Viewer');
    if (!assetViewer) throw new Error('asset role "Viewer" not found');
    const grantResponse = await request.post(`/api/asset-sets/${assetSet.id}/roles`, {
      headers: SEC_FETCH,
      data: { user_id: actor.id, role_id: assetViewer.id },
    });
    expect(grantResponse.status(), await grantResponse.text()).toBe(201);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();
    await expect(page.locator('#nav-assets')).toBeVisible();
    await expect(assetFieldRow).toContainText(`${restrictedAssetTag} - ${restrictedAssetTitle}`);

    await page.goto('/assets');
    await expect(page.getByTestId('asset-browser')).toBeVisible();
    await page.locator('#asset-set-select').click();
    await expect(page.locator(`#asset-set-select-option-${assetSet.id}`)).toBeVisible();
    await expect(page.getByTestId('asset-set-select-option')).toHaveCount(1);
  } finally {
    await context.close();
    await request
      .delete(`/api/configuration-sets/${configSet.id}`, {
        headers: SEC_FETCH,
      })
      .catch(() => {});
    await request.delete(`/api/screens/${screen.id}`, { headers: SEC_FETCH }).catch(() => {});
    for (const fieldID of createdFieldIDs.reverse()) {
      await deleteCustomFieldViaAPI(request, fieldID).catch(() => {});
    }
    await request
      .delete(`/api/admin/asset-sets/${assetSet.id}`, {
        headers: SEC_FETCH,
      })
      .catch(() => {});
  }
});
