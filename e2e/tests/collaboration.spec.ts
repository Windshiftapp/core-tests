import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  type BrowserContext,
  expect,
  type Page,
  test,
} from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Two-user item collaboration — live comment visibility and stale-state safety.
 *
 * Why this spec exists:
 *   - `notifications.spec.ts` covers admin's tab pulling in comments from a
 *     second API session, but never opens a real browser page for that second
 *     user. Cross-tab divergence (e.g., one tab's poller running while the
 *     other one is idle) is invisible to that test.
 *   - `mentions.spec.ts` exercises mention notifications without ever asserting
 *     what the second user actually *sees* in the DOM.
 *
 * Risks covered here:
 *   - Two live tabs polling the same item — both directions must converge.
 *   - "Stale state" actions: an item open in one tab keeps showing the
 *     composer after the other tab deletes it; the comment POST must be
 *     rejected by the backend (no orphan writes).
 *
 * Poll cadence: notifications.js ticks every 30s active / 5m idle (see
 * frontend/src/lib/stores/notifications.js). Comments.svelte taps into the
 * notification bus, so cross-tab comment sync is gated by the *other tab's*
 * notification poller. Give each assertion 40s headroom for one full tick.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const POLL_WAIT_MS = 40_000;
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

interface MemberSession {
  context: BrowserContext;
  page: Page;
  apiCtx: APIRequestContext;
  userId: number;
  username: string;
  email: string;
  password: string;
}

async function loginMember(
  apiCtx: APIRequestContext,
  username: string,
  password: string
): Promise<void> {
  const resp = await apiCtx.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: { email_or_username: username, password, remember_me: false },
  });
  expect(resp.ok(), `member login failed (status ${resp.status()})`).toBeTruthy();
}

/**
 * Create a fresh member, assign them Editor on the given workspace, and open
 * a browser context authenticated as them. The returned `apiCtx` is the
 * context's own request handle (`context.request`), so API calls share
 * cookies with the page — exactly mirroring what the browser tab would do.
 * Caller is responsible for tearing the context down.
 */
async function setUpMember(
  adminRequest: APIRequestContext,
  browser: import('@playwright/test').Browser,
  workspaceId: number,
  suffix: string
): Promise<MemberSession> {
  const userData = generateUser(suffix);
  // Inline create with full error context — the shared helper's expect failure
  // doesn't surface the response body, which we need when two parallel workers
  // collide on race-sensitive resources (e.g. the user-counter audit row).
  const createResp = await adminRequest.post(`${BASE_URL}/api/users`, {
    headers: SEC_FETCH,
    data: {
      email: userData.email,
      username: userData.username,
      first_name: userData.first_name,
      last_name: userData.last_name,
      password: userData.password_hash,
    },
  });
  expect(
    createResp.ok(),
    `user create failed (${createResp.status()}): ${await createResp.text()}`
  ).toBeTruthy();
  const member = await createResp.json();
  const activateResp = await adminRequest.post(`${BASE_URL}/api/users/${member.id}/activate`, {
    headers: SEC_FETCH,
  });
  expect(
    activateResp.ok(),
    `user activate failed (${activateResp.status()}): ${await activateResp.text()}`
  ).toBeTruthy();

  // Assign Editor role so the member has item.view + item.comment + item.edit
  // on the shared workspace. (Delete remains admin-only; that's fine here.)
  const rolesResp = await adminRequest.get('/api/workspace-roles', {
    headers: SEC_FETCH,
  });
  expect(rolesResp.ok()).toBeTruthy();
  const rolesBody = await rolesResp.json();
  const roles: Array<{ id: number; name: string }> = rolesBody.data ?? rolesBody;
  const editorRole = roles.find((r) => r.name === 'Editor');
  if (!editorRole) throw new Error('Editor workspace role not found');
  const editorId = editorRole.id;
  const assignResp = await adminRequest.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: {
      user_id: member.id,
      workspace_id: workspaceId,
      role_id: editorId,
    },
  });
  expect(assignResp.ok(), `assign Editor failed: ${assignResp.status()}`).toBeTruthy();

  // Browser context: empty storageState so the member's browser is not
  // accidentally logged in as the admin via the project-level cookie file.
  const context = await browser.newContext({
    storageState: { cookies: [], origins: [] },
    baseURL: BASE_URL,
  });
  // Log in via the context's own request handle. Cookies set on this
  // request handle apply to the whole context, so subsequent page navigations
  // and member.apiCtx.* calls go out authenticated.
  await loginMember(context.request, userData.username, userData.password_hash);
  const page = await context.newPage();

  return {
    context,
    page,
    apiCtx: context.request,
    userId: member.id,
    username: userData.username,
    email: userData.email,
    password: userData.password_hash,
  };
}

async function teardownMember(session: MemberSession): Promise<void> {
  await session.page.close().catch(() => {});
  await session.context.close().catch(() => {});
}

test.describe('Two-user collaboration: live comments + stale state', () => {
  test('comments posted in one tab surface in the other tab within the poll window, bidirectionally', async ({
    page,
    request,
    browser,
  }) => {
    // Two poll windows (one per direction) + setup overhead.
    test.setTimeout(180_000);

    // Username = `e2euser${suffix}` and is capped at 32 chars (handlers/users.go).
    // Keep the suffix ≤ 25 chars; 13-digit timestamps already eat 13.
    const suffix = `clv${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    const member = await setUpMember(request, browser, ws.id, suffix);
    try {
      // Admin (default authed page) opens the item.
      const adminEventsReady = page.waitForResponse((response) => {
        const url = new URL(response.url());
        return (
          response.request().method() === 'GET' &&
          url.pathname === `/api/items/${item.id}/events` &&
          response.ok()
        );
      });
      await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
      await adminEventsReady;
      await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(0);

      // Member opens the same item in their own context.
      const memberEventsReady = member.page.waitForResponse((response) => {
        const url = new URL(response.url());
        return (
          response.request().method() === 'GET' &&
          url.pathname === `/api/items/${item.id}/events` &&
          response.ok()
        );
      });
      await member.page.goto(`/workspaces/${ws.id}/items/${item.id}`);
      await memberEventsReady;
      await expect(member.page.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });
      await expect(member.page.locator('[data-testid="comment-item"]')).toHaveCount(0);

      // ── Direction 1: member posts; admin's tab picks it up. ───────────
      const fromMember = `From member ${Date.now()}`;
      const memberPost = await member.apiCtx.post(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
        data: { content: fromMember, is_private: false },
      });
      expect(memberPost.ok(), `member comment POST failed (${memberPost.status()})`).toBeTruthy();

      // Admin's tab — comment + "new" badge (because admin is not the author).
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(1, {
        timeout: POLL_WAIT_MS,
      });
      await expect(page.locator('[data-testid="comment-item"]')).toContainText(fromMember);
      const adminBadge = page.locator('[data-testid="new-comments-badge"]');
      await expect(adminBadge).toBeVisible({ timeout: 5_000 });
      await expect(adminBadge).toHaveAttribute('data-new-count', '1');

      // Member's tab — the same comment shows up (member is the author, so
      // no "new" badge on their side).
      await expect(member.page.locator('[data-testid="comment-item"]')).toHaveCount(1, {
        timeout: POLL_WAIT_MS,
      });
      await expect(member.page.locator('[data-testid="new-comments-badge"]')).toHaveCount(0);

      // ── Direction 2: admin posts; member's tab picks it up. ───────────
      // Admin clicks the badge to clear it before posting, so the next badge
      // assertion on the admin side starts from a clean slate.
      await adminBadge.click();
      await expect(adminBadge).toHaveCount(0);

      const fromAdmin = `From admin ${Date.now()}`;
      const adminPost = await request.post(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
        data: { content: fromAdmin, is_private: false },
      });
      expect(adminPost.ok(), `admin comment POST failed (${adminPost.status()})`).toBeTruthy();

      // Member's tab — picks up the new comment + raises the badge.
      await expect(member.page.locator('[data-testid="comment-item"]')).toHaveCount(2, {
        timeout: POLL_WAIT_MS,
      });
      await expect(
        member.page.locator('[data-testid="comment-item"]').filter({ hasText: fromAdmin })
      ).toBeVisible();
      const memberBadge = member.page.locator('[data-testid="new-comments-badge"]');
      await expect(memberBadge).toBeVisible({ timeout: 5_000 });
      await expect(memberBadge).toHaveAttribute('data-new-count', '1');

      // Admin's tab — also reflects 2 comments but no badge (admin authored
      // the second one).
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(2, {
        timeout: POLL_WAIT_MS,
      });
      await expect(page.locator('[data-testid="new-comments-badge"]')).toHaveCount(0);
    } finally {
      await teardownMember(member);
    }
  });

  test('member with item open cannot comment after admin deletes it (stale state rejected by backend)', async ({
    request,
    browser,
  }) => {
    test.setTimeout(120_000);

    const suffix = `cls${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    const member = await setUpMember(request, browser, ws.id, suffix);
    try {
      // Member opens the item — composer is visible, item is in their view.
      // The rendered detail can precede EventSource subscription under load.
      // Wait for the streaming response so the subsequent delete cannot occur
      // before this member is actually subscribed to the item's topic.
      const itemEventsResponse = member.page.waitForResponse((response) => {
        const url = new URL(response.url());
        return (
          response.request().method() === 'GET' &&
          url.pathname === `/api/items/${item.id}/events` &&
          response.ok()
        );
      });
      await member.page.goto(`/workspaces/${ws.id}/items/${item.id}`);
      await expect(member.page.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });
      await itemEventsResponse;

      // Admin deletes the item from their own session. The member's tab is
      // about to receive the `deleted` SSE event (WI-484) and close the detail;
      // we assert both the backend rejection and that UI tear-down below.
      const deleteResp = await request.delete(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
      });
      expect(
        deleteResp.ok() || deleteResp.status() === 204,
        `admin item delete failed: ${deleteResp.status()}`
      ).toBeTruthy();

      // Member attempts to comment from their stale UI. We use the member's
      // API context (same session that the browser tab is authenticated
      // against) — this is the exact request the browser would issue when the
      // user hits "Comment" in the composer.
      const staleComment = await member.apiCtx.post(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
        data: {
          content: 'Trying to comment on a deleted item',
          is_private: false,
        },
      });

      // Backend must refuse: the item no longer exists. Either 404 (not
      // found) or 403 (permission resolution against a missing item) is
      // acceptable — what's NOT acceptable is a 2xx success.
      expect(
        staleComment.ok(),
        `stale comment POST unexpectedly succeeded (${staleComment.status()})`
      ).toBeFalsy();
      expect([403, 404]).toContain(staleComment.status());

      // Server view of the comment list should be empty too (defensive
      // check: nothing got written despite the failed POST).
      const list = await request.get(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
      });
      // The item is gone — list also 404s. Either way, no comments were
      // persisted from the stale tab.
      if (list.ok()) {
        const body = await list.json();
        const arr = (body.data ?? body.comments ?? body) as unknown[];
        expect(arr.length).toBe(0);
      } else {
        expect([403, 404]).toContain(list.status());
      }

      // UI-driven half of the same scenario. Before WI-484 the member's tab
      // kept rendering the stale item and the test drove a comment submit to
      // assert the failed POST surfaced an inline error. WI-484 now makes the
      // `deleted` SSE event close the detail outright, so there is no longer a
      // stale composer to type into — which is a strictly stronger guarantee
      // against a fake-success state. Assert the composer is gone instead.
      await expect(member.page.locator('[data-testid="comments-section"]')).toBeHidden({
        timeout: 15_000,
      });
      // And nothing optimistically appended a phantom comment before tear-down.
      await expect(member.page.locator('[data-testid="comment-item"]')).toHaveCount(0);
    } finally {
      await teardownMember(member);
    }
  });
});
