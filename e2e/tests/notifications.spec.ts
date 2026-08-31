import { createItemViaAPI, createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Notification polling + toast + live-comment tests.
 *
 * The poller ticks every 30 s when the user is active (see usePoller.svelte.js
 * and notifications.js). Each assertion that waits for a tick uses a 40 s
 * budget, and each test overrides the default 60 s test timeout.
 */
test.describe('Notifications: toast + live item comments', () => {
  // Poll cadence is 30 s; give ourselves headroom for the tick + render.
  const POLL_WAIT_MS = 40_000;

  test('mention notification surfaces as an info toast', async ({ page, request }) => {
    test.setTimeout(90_000);

    await page.goto('/');
    // MainApp is mounted when the global create button exists — this is also
    // the moment startNotificationPoller() has been called.
    await expect(page.locator('#global-create-button')).toBeVisible({ timeout: 15_000 });

    const title = `E2E Mention ${Date.now()}`;
    const message = 'Agent @admin mentioned you in a comment';

    // Self-create a mention notification. The backend forces user_id = caller,
    // so this appears in the admin's inbox; the frontend poller picks it up
    // and the toast allowlist (`mention`) triggers a toast.
    const resp = await request.post('/api/notifications', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        type: 'mention',
        title,
        message,
        action_url: '/notifications',
        timestamp: new Date().toISOString(),
      },
    });
    expect(resp.ok()).toBeTruthy();

    // The toast appears within one poll window.
    const toast = page.locator('[data-testid="toast"]').filter({ hasText: title });
    await expect(toast).toBeVisible({ timeout: POLL_WAIT_MS });
    await expect(toast).toHaveAttribute('data-toast-variant', 'info');
    await expect(toast).toContainText(message);
  });

  test('assignment notification also toasts; plain "info" type does not', async ({
    page,
    request,
  }) => {
    test.setTimeout(90_000);

    await page.goto('/');
    await expect(page.locator('#global-create-button')).toBeVisible({ timeout: 15_000 });

    const assignTitle = `E2E Assigned ${Date.now()}`;
    const infoTitle = `E2E Info ${Date.now()}`;

    // Assignment should toast (allowlisted).
    await request.post('/api/notifications', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        type: 'assignment',
        title: assignTitle,
        message: 'You were assigned an item',
        action_url: '/',
        timestamp: new Date().toISOString(),
      },
    });

    // Plain info should NOT toast — it stays in the tray only.
    await request.post('/api/notifications', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        type: 'info',
        title: infoTitle,
        message: 'Background info event',
        action_url: '/',
        timestamp: new Date().toISOString(),
      },
    });

    await expect(
      page.locator('[data-testid="toast"]').filter({ hasText: assignTitle })
    ).toBeVisible({ timeout: POLL_WAIT_MS });

    // After seeing the assignment toast, the info one must not have appeared.
    const infoToast = page.locator('[data-testid="toast"]').filter({ hasText: infoTitle });
    await expect(infoToast).toHaveCount(0);
  });

  test('open item pulls in new comments from another author and shows the badge', async ({
    page,
    request,
    playwright,
  }) => {
    test.setTimeout(120_000);

    // Unique suffix so retries don't collide on user/workspace unique constraints.
    const suffix = `notif-live-${Date.now()}`;

    // Set up a workspace + item via API (fast, no UI).
    const ws = generateWorkspace(suffix);
    const workspace = await createWorkspaceViaAPI(request, ws);
    const wsId: number = workspace.id;

    const itemData = generateItem(wsId, suffix);
    const item = await createItemViaAPI(request, wsId, {
      title: itemData.title,
      description: itemData.description,
    });
    const itemId: number = item.id;

    // A second authenticated user who will post the comment. The
    // comment-create endpoint always uses the caller's session as the author
    // (no body-supplied author_id — see comment.go), so we need a real
    // second session to exercise the "comment from another author" badge path.
    const userB = generateUser(`commenter-${Date.now()}`);
    await createUserViaAPI(request, userB);
    const userBCtx = await playwright.request.newContext({
      baseURL: process.env.BASE_URL || 'http://localhost:8080',
      storageState: { cookies: [], origins: [] },
    });
    try {
      const loginResp = await userBCtx.post('/api/auth/login', {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
        data: {
          email_or_username: userB.username,
          password: userB.password_hash,
          remember_me: false,
        },
      });
      expect(loginResp.ok(), `userB login failed (status ${loginResp.status()})`).toBeTruthy();

      // Open the item detail view (renders as a full page under /workspaces/:id/items/:id).
      await page.goto(`/workspaces/${wsId}/items/${itemId}`);
      // Comment composer present => Comments.svelte mounted and polling.
      // Comments section is rendered by Comments.svelte (ProseMirror placeholders
      // aren't real HTML `placeholder` attrs, so target the section wrapper).
      await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });

      // No comments yet.
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(0);

      // Post a comment as user B from their own session.
      const content = `Hi from agent B ${Date.now()}`;
      const commentResp = await userBCtx.post(`/api/items/${itemId}/comments`, {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
        data: {
          content,
          is_private: false,
        },
      });
      expect(
        commentResp.ok(),
        `userB comment POST failed (status ${commentResp.status()})`
      ).toBeTruthy();

      // Within one poll window the comment shows up inline — no reload.
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(1, {
        timeout: POLL_WAIT_MS,
      });
      await expect(page.locator('[data-testid="comment-item"]')).toContainText(content);

      // And the "N new" badge appears because the author is user B, not admin.
      const badge = page.locator('[data-testid="new-comments-badge"]');
      await expect(badge).toBeVisible({ timeout: 5_000 });
      await expect(badge).toHaveAttribute('data-new-count', '1');

      // Clicking the badge dismisses it.
      await badge.click();
      await expect(badge).toHaveCount(0);
    } finally {
      await userBCtx.dispose();
    }
  });

  test('submitting my own comment does not raise the "new" badge', async ({ page, request }) => {
    test.setTimeout(120_000);

    const suffix = `notif-self-${Date.now()}`;
    const ws = generateWorkspace(suffix);
    const workspace = await createWorkspaceViaAPI(request, ws);
    const wsId: number = workspace.id;

    const itemData = generateItem(wsId, suffix);
    const item = await createItemViaAPI(request, wsId, {
      title: itemData.title,
      description: itemData.description,
    });
    const itemId: number = item.id;

    await page.goto(`/workspaces/${wsId}/items/${itemId}`);
    // Comments section is rendered by Comments.svelte (ProseMirror placeholders
    // aren't real HTML `placeholder` attrs, so target the section wrapper).
    await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({ timeout: 15_000 });

    // Admin posts a comment on their own item — author_id = admin's id.
    // Look up the current user's id via the whoami endpoint.
    const meResp = await request.get('/api/auth/me', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
    });
    expect(meResp.ok()).toBeTruthy();
    const me = await meResp.json();
    const myId: number = me.user.id;

    await request.post(`/api/items/${itemId}/comments`, {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        content: 'My own note',
        author_id: myId,
        is_private: false,
      },
    });

    // The comment still appears on the next poll…
    await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(1, {
      timeout: POLL_WAIT_MS,
    });
    // …but the badge must not show, since the author is the current user.
    await expect(page.locator('[data-testid="new-comments-badge"]')).toHaveCount(0);
  });
});
