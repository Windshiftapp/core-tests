import type { APIRequestContext } from '@playwright/test';
import {
  createItemViaAPI,
  createLinkViaAPI,
  createUserViaAPI,
  createWorkspaceViaAPI,
  listLinkTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function roleId(request: APIRequestContext, name: string): Promise<number> {
  const response = await request.get('/api/workspace-roles', { headers: SEC_FETCH });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  const role = (body.data ?? body).find((candidate: { name: string }) => candidate.name === name);
  expect(role).toBeDefined();
  return role.id;
}

async function assignRole(
  request: APIRequestContext,
  userId: number,
  workspaceId: number,
  workspaceRoleId: number
): Promise<void> {
  const response = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userId, workspace_id: workspaceId, role_id: workspaceRoleId },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function revokeRole(
  request: APIRequestContext,
  userId: number,
  workspaceId: number,
  workspaceRoleId: number
): Promise<void> {
  const response = await request.delete(
    `/api/users/${userId}/workspaces/${workspaceId}/roles/${workspaceRoleId}`,
    { headers: SEC_FETCH }
  );
  expect(response.ok(), await response.text()).toBeTruthy();
}

test('linked test cases stay invisible without test.view and reappear after permission grant', async ({
  request,
  browser,
}) => {
  const stamp = `ltp-${Date.now().toString(36)}`;
  const token = `RESTRICTED_CASE_${Date.now()}`;
  const secondSearchTerm = `SECOND_PROBE_${Date.now()}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(stamp));
  const item = await createItemViaAPI(request, workspace.id, { title: `Visible item ${stamp}` });

  const testCaseResponse = await request.post(`/api/workspaces/${workspace.id}/test-cases`, {
    headers: SEC_FETCH,
    data: {
      title: `${token} ${secondSearchTerm}`,
      preconditions: `private preconditions ${token}`,
      priority: 'medium',
      status: 'active',
    },
  });
  expect(testCaseResponse.status(), await testCaseResponse.text()).toBe(201);
  const testCase = (await testCaseResponse.json()) as { id: number };

  const linkTypes = await listLinkTypesViaAPI(request);
  const testsLinkType = linkTypes.find((linkType) => linkType.name === 'Tests');
  if (!testsLinkType) throw new Error('Tests link type not found');
  await createLinkViaAPI(request, {
    link_type_id: testsLinkType.id,
    source_type: 'item',
    source_id: item.id,
    target_type: 'test_case',
    target_id: testCase.id,
  });

  const viewerData = generateUser(`${stamp}-viewer`);
  const viewer = await createUserViaAPI(request, viewerData);
  const viewerRoleId = await roleId(request, 'Viewer');
  const editorRoleId = await roleId(request, 'Editor');
  const testerRoleId = await roleId(request, 'Tester');

  // Explicit Viewer gives the browser actor item.view. Assigning Editor to
  // the admin closes the implicit Editor/Tester ladder for everyone else, so
  // the actor has no effective test.view until Tester is granted below.
  await assignRole(request, viewer.id, workspace.id, viewerRoleId);
  const meResponse = await request.get('/api/auth/me', { headers: SEC_FETCH });
  expect(meResponse.ok()).toBeTruthy();
  const me = await meResponse.json();
  await assignRole(request, me.user.id, workspace.id, editorRoleId);

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  try {
    const loginResponse = await context.request.post('/api/auth/login', {
      headers: SEC_FETCH,
      data: {
        email_or_username: viewerData.username,
        password: viewerData.password_hash,
        remember_me: false,
      },
    });
    expect(loginResponse.ok(), await loginResponse.text()).toBeTruthy();

    const page = await context.newPage();
    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();

    const linkRows = page.getByTestId('linked-item-row');
    await expect
      .poll(async () => (await linkRows.allTextContents()).join('\n'))
      .not.toContain(token);

    // Exercise the rendered picker too: test cases that cannot be viewed must
    // not be discoverable through its search results.
    await page.getByTestId('add-link-button').first().click();
    await expect(page.getByTestId('link-modal')).toBeVisible();
    await page.locator('#link-type-picker').click();
    await page.getByTestId(`link-type-option-${testsLinkType.id}`).click();

    // Establish a rendered positive control, then remove test.view and issue
    // a different matching query. The old result stays rendered until the new
    // search completes, so reaching zero rows proves the denied UI search ran.
    await assignRole(request, viewer.id, workspace.id, testerRoleId);
    const visibleSearchResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/links/search' &&
        url.searchParams.get('q') === token
      );
    });
    await page.locator('#link-target-search').fill(token);
    expect((await visibleSearchResponse).ok()).toBeTruthy();
    await expect(page.getByTestId('link-search-result')).toContainText(token, { timeout: 20_000 });
    await revokeRole(request, viewer.id, workspace.id, testerRoleId);
    const deniedSearchResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/links/search' &&
        url.searchParams.get('q') === secondSearchTerm
      );
    });
    await page.locator('#link-target-search').fill(secondSearchTerm);
    expect((await deniedSearchResponse).ok()).toBeTruthy();
    await expect(page.getByTestId('link-search-result')).toHaveCount(0);

    // Restore test visibility as fixture setup, then verify the persisted
    // relationship appears through a fresh rendered UI read.
    await assignRole(request, viewer.id, workspace.id, testerRoleId);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();
    await expect.poll(async () => (await linkRows.allTextContents()).join('\n')).toContain(token);
  } finally {
    await context.close();
  }
});
