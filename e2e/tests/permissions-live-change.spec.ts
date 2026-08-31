import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Live permission revocation — actions denied without a reload.
 *
 * Scenario (test plan §6):
 *   1. Member opens an item detail page in their browser context — UI
 *      loads, composer is visible, all the action affordances are in the
 *      DOM at full Editor permission.
 *   2. Admin revokes the member's Editor role on the workspace in a
 *      separate session.
 *   3. Member tries to comment, edit, and transition from the (now stale)
 *      tab. The backend MUST reject each action — the page-load snapshot
 *      doesn't get a permission grace period.
 *
 * Risk covered:
 *   - Permission-cache invalidation on role revoke (permission_cache.go
 *     snapshots affected users before DELETE cascades).
 *   - "Visible therefore allowed" leakage: the UI composer doesn't gate
 *     the action; only the server's `canEditItem` check does.
 *   - The 404-not-403 invariant from the Security Policy (CLAUDE.md):
 *     workspace permission failures on item endpoints return 404 to avoid
 *     leaking item existence.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function getRoleId(request: APIRequestContext, name: string): Promise<number> {
  const resp = await request.get('/api/workspace-roles', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  const roles: Array<{ id: number; name: string }> = body.data ?? body;
  const role = roles.find((r) => r.name === name);
  if (!role) throw new Error(`role "${name}" missing from seed`);
  return role.id;
}

async function createMember(
  request: APIRequestContext,
  suffix: string
): Promise<{ id: number; username: string; password: string }> {
  const data = generateUser(suffix);
  const resp = await request.post(`${BASE_URL}/api/users`, {
    headers: SEC_FETCH,
    data: {
      email: data.email,
      username: data.username,
      first_name: data.first_name,
      last_name: data.last_name,
      password: data.password_hash,
    },
  });
  expect(resp.ok(), `member create: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const user = await resp.json();
  await request.post(`${BASE_URL}/api/users/${user.id}/activate`, {
    headers: SEC_FETCH,
  });
  return { id: user.id, username: data.username, password: data.password_hash };
}

test.describe('Permission revocation while session is live', () => {
  test('admin revoke → member edit/comment/transition all return 404 on the stale tab', async ({
    request,
    browser,
  }) => {
    test.setTimeout(60_000);

    const suffix = `plc${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    // Two users:
    //   - gateUser: a placeholder Viewer assignment. Per permission_cache.go
    //     the workspace's everyone-Viewer fallback is active ONLY while
    //     no user has the Viewer role; this assignment switches the
    //     fallback off so revoking `member`'s role below actually leaves
    //     them with zero permission, instead of dropping back into the
    //     workspace-open default.
    //   - member: the user under test. Editor covers item.view +
    //     item.edit + item.comment.
    const gateUser = await createMember(request, `${suffix}-g`);
    const member = await createMember(request, suffix);

    const viewerRoleId = await getRoleId(request, 'Viewer');
    const editorRoleId = await getRoleId(request, 'Editor');

    const gateAssign = await request.post('/api/workspace-roles/assign', {
      headers: SEC_FETCH,
      data: {
        user_id: gateUser.id,
        workspace_id: ws.id,
        role_id: viewerRoleId,
      },
    });
    expect(gateAssign.ok(), `assign gate Viewer: ${gateAssign.status()}`).toBeTruthy();

    const assignResp = await request.post('/api/workspace-roles/assign', {
      headers: SEC_FETCH,
      data: { user_id: member.id, workspace_id: ws.id, role_id: editorRoleId },
    });
    expect(assignResp.ok(), `assign Editor: ${assignResp.status()}`).toBeTruthy();

    // Member context with empty storage state, then API login. The
    // resulting cookie applies to both context.request and any page in
    // the context — exactly what the browser tab and the user's "send
    // comment" button would each use.
    const memberCtx = await browser.newContext({
      storageState: { cookies: [], origins: [] },
      baseURL: BASE_URL,
    });
    try {
      const login = await memberCtx.request.post('/api/auth/login', {
        headers: SEC_FETCH,
        data: {
          email_or_username: member.username,
          password: member.password,
          remember_me: false,
        },
      });
      expect(login.ok(), `member login: ${login.status()}`).toBeTruthy();

      // Open the item detail in the member's tab — the UI hydrates with
      // Editor permission. We're not asserting any UI affordances here;
      // the contract under test is that the SERVER refuses subsequent
      // actions even though the DOM still shows the composer.
      const memberPage = await memberCtx.newPage();
      await memberPage.goto(`/workspaces/${ws.id}/items/${item.id}`);
      await expect(memberPage.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });
      await expect(memberPage.getByTestId('comment-composer')).toHaveAttribute(
        'data-ready',
        'true',
        { timeout: 15_000 }
      );

      // Prepare a draft while the member is still authorized. The later
      // submit exercises the stale UI after the role has been revoked.
      const composer = memberPage.getByTestId('comment-editor');
      await composer.click();
      await memberPage.keyboard.insertText('UI-driven post-revoke comment');
      await memberPage.keyboard.press('Tab');

      // Sanity check: member CAN read the item while Editor is in place.
      const preGet = await memberCtx.request.get(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
      });
      expect(preGet.ok(), `pre-revoke GET: ${preGet.status()}`).toBeTruthy();

      // Keep this browser tab on its authorized snapshot. Otherwise the
      // background reconciler can observe the later 404 and close the detail
      // before the stale composer is exercised. Only page-level item reads are
      // held; every mutation and direct API assertion still reaches the server.
      const staleItemSnapshot = await preGet.json();
      await memberPage.route(
        (url) => url.pathname === `/api/items/${item.id}`,
        async (route) => {
          if (route.request().method() !== 'GET') {
            await route.continue();
            return;
          }
          await route.fulfill({ status: 200, json: staleItemSnapshot });
        }
      );

      // Admin revokes the member's only role. Per workspace_roles.go,
      // the handler snapshots affected users before the DELETE so the
      // permission cache invalidation runs synchronously.
      const revoke = await request.delete(
        `/api/users/${member.id}/workspaces/${ws.id}/roles/${editorRoleId}`,
        { headers: SEC_FETCH }
      );
      expect(revoke.ok() || revoke.status() === 204, `revoke: ${revoke.status()}`).toBeTruthy();

      // From the member's stale session, every action — read, comment,
      // edit, transition — must return 404. The gateUser's Viewer
      // assignment above keeps the everyone-Viewer fallback off, so the
      // member has zero permission after revoke. Security Policy:
      // workspace permission failures on item-scoped endpoints return
      // 404, not 403, to avoid leaking item existence.
      const postGet = await memberCtx.request.get(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
      });
      expect(
        postGet.ok(),
        `post-revoke GET unexpectedly succeeded (${postGet.status()})`
      ).toBeFalsy();
      expect(postGet.status()).toBe(404);

      const comment = await memberCtx.request.post(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
        data: { content: 'should be rejected', is_private: false },
      });
      expect(
        comment.ok(),
        `post-revoke comment unexpectedly succeeded (${comment.status()})`
      ).toBeFalsy();
      expect(comment.status()).toBe(404);

      const edit = await memberCtx.request.put(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
        data: { description: 'should be rejected' },
      });
      expect(edit.ok(), `post-revoke edit unexpectedly succeeded (${edit.status()})`).toBeFalsy();
      expect(edit.status()).toBe(404);

      // Pick any seeded workspace status that's not the item's current
      // one so the transition isn't a no-op — we want the canEdit gate
      // to fail before the workflow validator even runs.
      const statusesResp = await request.get(`/api/workspaces/${ws.id}/statuses`, {
        headers: SEC_FETCH,
      });
      const statuses = (await statusesResp.json()) as Array<{ id: number }>;
      const targetStatusId = statuses[1]?.id ?? statuses[0]?.id;
      const transition = await memberCtx.request.post(`/api/items/${item.id}/transition`, {
        headers: SEC_FETCH,
        data: { to_status_id: targetStatusId },
      });
      expect(
        transition.ok(),
        `post-revoke transition unexpectedly succeeded (${transition.status()})`
      ).toBeFalsy();
      expect(transition.status()).toBe(404);

      // Admin-side state must be intact: the item still exists, the
      // status hasn't moved, no comment was written. This pins "no
      // partial writes" on the revoke race.
      const adminGet = await request.get(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
      });
      expect(adminGet.ok()).toBeTruthy();
      const adminItem = await adminGet.json();
      expect(adminItem.description).toBe(itemData.description);

      const adminComments = await request.get(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
      });
      expect(adminComments.ok()).toBeTruthy();
      const cbody = await adminComments.json();
      const arr = (cbody?.data ?? cbody?.comments ?? cbody ?? []) as unknown[];
      expect(arr.length).toBe(0);

      // UI-driven half: drive the stale Comment button. The composer is
      // still mounted (it hydrated at Editor permission), so a regression
      // that silently appended an optimistic comment row instead of
      // surfacing the rejection would fail this block.
      const staleCommentRespPromise = memberPage.waitForResponse(
        (resp) =>
          resp.url().includes(`/api/items/${item.id}/comments`) &&
          resp.request().method() === 'POST',
        { timeout: 10_000 }
      );
      const submitBtn = memberPage.getByTestId('comment-submit');
      await expect(submitBtn).toBeEnabled({ timeout: 5_000 });
      await submitBtn.click();
      const staleCommentResp = await staleCommentRespPromise;
      expect(staleCommentResp.status()).toBe(404);

      // After the failed POST, no comment row should be appended (no fake
      // optimistic-success state), and the error AlertBox in the comments
      // section must show the failedToCreate string. Comments.svelte writes
      // `error = t('comments.failedToCreate')` which the en locale resolves
      // to "Failed to post comment".
      await expect(memberPage.locator('[data-testid="comment-item"]')).toHaveCount(0);
      await expect(memberPage.getByTestId('comments-error')).toContainText(
        /failed to post comment/i,
        { timeout: 5_000 }
      );
    } finally {
      await memberCtx.close();
    }
  });
});
