import { createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Knowledge Pages — permission-sensitive flows.
 *
 * The Go unit suite already covers per-page ACL evaluation in
 * `internal/services/page_permission_service_test.go`. What's missing —
 * and what the audit doc flagged — is the end-to-end wiring through the
 * cookie-auth surface: that a workspace member without page access
 * actually gets 404 from /api/workspaces/{id}/pages/{pageId}, that the
 * tree omits restricted pages for them, and that the knowledge search
 * endpoint suppresses restricted hits. Those HTTP contracts live in
 * tests/e2e_security_contracts_test.go; this file keeps the user-facing
 * browser outcomes.
 *
 * The browser flows mirror the structure of permissions-cross-workspace.spec.ts.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function loginAs(ctx: APIRequestContext, username: string, password: string): Promise<void> {
  const resp = await ctx.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: { email_or_username: username, password, remember_me: false },
  });
  expect(resp.ok(), `login as ${username} failed (status ${resp.status()})`).toBeTruthy();
}

async function getRoleIdByName(ctx: APIRequestContext, name: string): Promise<number> {
  const resp = await ctx.get('/api/workspace-roles', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  const roles: Array<{ id: number; name: string }> = body.data ?? body;
  const role = roles.find((r) => r.name === name);
  expect(role, `workspace role "${name}" not found`).toBeDefined();
  if (!role) throw new Error(`workspace role "${name}" not found`);
  return role.id;
}

async function assignWorkspaceRole(
  ctx: APIRequestContext,
  userId: number,
  workspaceId: number,
  roleId: number
): Promise<void> {
  const resp = await ctx.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userId, workspace_id: workspaceId, role_id: roleId },
  });
  expect(
    resp.ok(),
    `role assign failed (status ${resp.status()}): ${await resp.text().catch(() => '')}`
  ).toBeTruthy();
}

async function createPage(
  ctx: APIRequestContext,
  workspaceId: number,
  data: { title: string; content?: string; parent_id?: number | null }
): Promise<{ id: number; title: string }> {
  const resp = await ctx.post(`/api/workspaces/${workspaceId}/pages`, {
    headers: SEC_FETCH,
    data: {
      title: data.title,
      content: data.content ?? '',
      parent_id: data.parent_id ?? null,
    },
  });
  expect(resp.ok(), `create page failed (${resp.status()})`).toBeTruthy();
  return resp.json();
}

async function setInheritance(
  ctx: APIRequestContext,
  workspaceId: number,
  pageId: number,
  inherit: boolean
): Promise<void> {
  const resp = await ctx.patch(`/api/workspaces/${workspaceId}/pages/${pageId}/inheritance`, {
    headers: SEC_FETCH,
    data: { inherit_permissions: inherit },
  });
  expect(resp.ok(), `set inheritance failed (${resp.status()})`).toBeTruthy();
}

test.describe('Knowledge Pages — permissions', () => {
  // Serial because all tests share one workspace, one open page, and one
  // restricted page. The setup cost (user creation + login) is non-trivial
  // and the assertions are independent per test.
  test.describe.configure({ mode: 'serial' });

  let workspaceId: number;
  let openPageId: number;
  let restrictedPageId: number;
  // Credentials are captured here so the browser-driven UI test below can
  // log in as the viewer in a separate context.
  let viewerUsername: string;
  let viewerPassword: string;

  test.beforeAll(async ({ request: adminRequest }) => {
    // Admin sets up: a gated workspace, an open page, and a restricted
    // page (inherit_permissions=false → no implicit access for any role).
    const wsData = generateWorkspace(`knowledge-perm-${Date.now()}`);
    const ws = await createWorkspaceViaAPI(adminRequest, wsData);
    workspaceId = ws.id;

    // Use a short username — generateUser produces something like
    // "user-knowledge-viewer-..." which exceeds the user/email constraints
    // when stamped with two suffixes. Keep stamp lean for clarity.
    const viewerUserData = generateUser(`pgvw-${Date.now()}`);
    const viewerUser = await createUserViaAPI(adminRequest, viewerUserData);
    viewerUsername = viewerUserData.username;
    viewerPassword = viewerUserData.password_hash;

    // Assign Viewer to the viewer user — this also flips the workspace
    // into gated mode (an explicit Viewer assignment kills the
    // "everyone has Viewer" fallback).
    const viewerRoleId = await getRoleIdByName(adminRequest, 'Viewer');
    await assignWorkspaceRole(adminRequest, viewerUser.id, ws.id, viewerRoleId);

    const open = await createPage(adminRequest, ws.id, {
      title: 'Public runbook',
      content: '# Public runbook\n\nopenpermzebra keyword for search.',
    });
    openPageId = open.id;
    const openPageResponse = await adminRequest.get(`/api/workspaces/${ws.id}/pages/${open.id}`, {
      headers: SEC_FETCH,
    });
    expect(openPageResponse.ok()).toBeTruthy();
    const openPage = (await openPageResponse.json()) as {
      content_hash: string;
    };
    const diagramResponse = await adminRequest.post(
      `/api/workspaces/${ws.id}/pages/${open.id}/diagrams`,
      {
        headers: SEC_FETCH,
        data: {
          name: 'Viewer-safe diagram',
          excalidraw: { elements: [], appState: {}, files: {} },
          placement: 'end',
          expected_content_hash: openPage.content_hash,
        },
      }
    );
    expect(
      diagramResponse.ok(),
      `seed Page diagram failed (${diagramResponse.status()})`
    ).toBeTruthy();

    const restricted = await createPage(adminRequest, ws.id, {
      title: 'Confidential',
      content: '# Confidential\n\nopenpermzebra keyword in restricted doc.',
    });
    restrictedPageId = restricted.id;

    // Break inheritance on the restricted page with no ACL grant → only
    // effective admins can see it. The Viewer must not.
    await setInheritance(adminRequest, ws.id, restrictedPageId, false);
  });

  test('viewer browser: tree omits restricted page and direct URL surfaces a safe denial state', async ({
    browser,
  }) => {
    // Build a viewer-authenticated browser context (separate cookie jar
    // from admin's storage state). The API-level checks above prove the
    // server side; this test pins the user-facing UI behavior.
    const viewerBrowserCtx = await browser.newContext({
      baseURL: BASE_URL,
      storageState: { cookies: [], origins: [] },
    });
    try {
      await loginAs(viewerBrowserCtx.request, viewerUsername, viewerPassword);

      const viewerPage = await viewerBrowserCtx.newPage();

      // (1) Pages index — the tree must include the open page and omit
      // the restricted one. data-page-id is the stable identifier on
      // PagesNavSidebar tree rows.
      await viewerPage.goto(`/workspaces/${workspaceId}/pages`);
      await viewerPage.waitForLoadState('networkidle');
      await expect(viewerPage.getByTestId(`page-tree-item-${openPageId}`)).toBeVisible({
        timeout: 10_000,
      });
      await expect(viewerPage.getByTestId(`page-tree-item-${restrictedPageId}`)).toHaveCount(0);

      // (2) Direct URL to the restricted page must NOT render the page
      // title/content. PagesView.loadPage sets `error =
      // t('pages.errorLoadPage')` on 404 and surfaces it in a [role="alert"]
      // element; selectedPage stays null so the title input doesn't mount.
      await viewerPage.goto(`/workspaces/${workspaceId}/pages/${restrictedPageId}`);
      await viewerPage.waitForLoadState('networkidle');
      await expect(viewerPage.locator('#page-title-input')).toHaveCount(0);
      await expect(viewerPage.getByTestId('page-error')).toBeVisible({
        timeout: 5_000,
      });
      // Defense in depth: the restricted page's body keyword must not
      // appear anywhere on the rendered page.
      await expect(viewerPage.getByTestId('pages-view')).not.toContainText(
        'openpermzebra keyword in restricted doc'
      );
    } finally {
      await viewerBrowserCtx.close();
    }
  });

  test('viewer browser renders a Page diagram without exposing edit controls', async ({
    browser,
  }) => {
    const viewerBrowserCtx = await browser.newContext({
      baseURL: BASE_URL,
      storageState: { cookies: [], origins: [] },
    });
    try {
      await loginAs(viewerBrowserCtx.request, viewerUsername, viewerPassword);
      const viewerPage = await viewerBrowserCtx.newPage();
      await viewerPage.goto(`/workspaces/${workspaceId}/pages/${openPageId}`);
      await viewerPage.waitForLoadState('networkidle');

      await expect(viewerPage.getByTestId('page-diagram-block')).toBeVisible({
        timeout: 10_000,
      });
      await expect(viewerPage.getByTestId('page-diagram-caption')).toHaveText(
        'Viewer-safe diagram'
      );
      await expect(viewerPage.getByTestId('excalidraw-block-edit')).toHaveCount(0);
      await expect(viewerPage.getByTestId('excalidraw-block-delete')).toHaveCount(0);
      await expect(viewerPage.getByTestId('milkdown-insert-diagram')).toHaveCount(0);
      await expect(viewerPage.getByTestId('page-mode-edit')).toHaveCount(0);
      await expect(viewerPage.locator('#page-title-input')).toBeDisabled();
    } finally {
      await viewerBrowserCtx.close();
    }
  });
});
