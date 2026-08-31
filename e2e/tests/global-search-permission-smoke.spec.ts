import type { APIRequestContext } from '@playwright/test';
import { createItemViaAPI, createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function roleId(request: APIRequestContext, name: string): Promise<number> {
  const response = await request.get('/api/workspace-roles', {
    headers: SEC_FETCH,
  });
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
) {
  const response = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: {
      user_id: userId,
      workspace_id: workspaceId,
      role_id: workspaceRoleId,
    },
  });
  expect(response.ok()).toBeTruthy();
}

test('global search renders only accessible items and opens the allowed result', async ({
  request,
  browser,
}) => {
  const stamp = `rc-search-${Date.now()}`;
  const token = `RC_SEARCH_${Date.now()}`;
  const visibleWorkspace = await createWorkspaceViaAPI(
    request,
    generateWorkspace(`${stamp}-visible`)
  );
  const hiddenWorkspace = await createWorkspaceViaAPI(
    request,
    generateWorkspace(`${stamp}-hidden`)
  );

  const viewerRoleId = await roleId(request, 'Viewer');
  const editorRoleId = await roleId(request, 'Editor');
  const visibleGate = await createUserViaAPI(request, generateUser(`${stamp}-visible-gate`));
  const hiddenGate = await createUserViaAPI(request, generateUser(`${stamp}-hidden-gate`));
  await assignRole(request, visibleGate.id, visibleWorkspace.id, viewerRoleId);
  await assignRole(request, hiddenGate.id, hiddenWorkspace.id, viewerRoleId);

  const outsiderData = generateUser(`${stamp}-outsider`);
  const outsider = await createUserViaAPI(request, outsiderData);
  await assignRole(request, outsider.id, visibleWorkspace.id, editorRoleId);

  const visibleItem = await createItemViaAPI(request, visibleWorkspace.id, {
    title: `${token} visible`,
  });
  const hiddenItem = await createItemViaAPI(request, hiddenWorkspace.id, {
    title: `${token} restricted`,
  });

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  try {
    const login = await context.request.post('/api/auth/login', {
      headers: SEC_FETCH,
      data: {
        email_or_username: outsiderData.username,
        password: outsiderData.password_hash,
        remember_me: false,
      },
    });
    expect(login.ok()).toBeTruthy();

    const page = await context.newPage();
    await page.goto('/search');
    await expect(page.getByTestId('global-search-page')).toBeVisible();
    await page.getByTestId('global-search-query').fill(token);
    await page.getByTestId('global-search-query').press('Enter');

    const visibleRow = page.getByTestId(`global-search-result-${visibleItem.id}`);
    await expect(visibleRow).toBeVisible();
    await expect(page.getByTestId(`global-search-result-${hiddenItem.id}`)).toHaveCount(0);

    await visibleRow.click();
    await expect(page).toHaveURL(
      new RegExp(`/workspaces/${visibleWorkspace.id}/items/${visibleItem.id}(?:$|[?#])`)
    );
  } finally {
    await context.close();
  }
});
