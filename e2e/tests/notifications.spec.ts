import { createItemViaAPI, createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Notification toast + live-comment tests.
 *
 * Notifications and open item views update over SSE (see notifications.js and
 * useItemEventStream). Each assertion that waits for the push uses a 40 s
 * budget, and each test overrides the default 60 s test timeout.
 */
test.describe('Notifications: toast + live item comments', () => {
  // Generous headroom for the SSE push + render on a slow runner.
  const POLL_WAIT_MS = 40_000;

  // Notifications share the admin inbox across parallel workers. A millisecond
  // timestamp is not unique enough: colliding titles produce multiple toasts
  // and make the title-filtered locators ambiguous.
  const uniqueSuffix = () =>
    `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;

  // Toast tests need an inbox that only they write to. The shared admin inbox
  // collects notifications from every parallel worker, and the toast stack
  // only renders the newest three, so the notification under test can be
  // pushed out of view. A fresh user per test keeps the stack deterministic.
  async function openToastPage(browser, request) {
    const data = generateUser(`notif-toast-${uniqueSuffix()}`);
    await createUserViaAPI(request, data);
    const context = await browser.newContext({
      baseURL: process.env.BASE_URL || 'http://localhost:8080',
      storageState: { cookies: [], origins: [] },
    });
    const login = await context.request.post('/api/auth/login', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        email_or_username: data.username,
        password: data.password_hash,
        remember_me: false,
      },
    });
    expect(login.ok(), `notification user login: ${login.status()}`).toBeTruthy();
    return { context, page: await context.newPage() };
  }

  test('opening an item marks an older notification beyond the default page as read', async ({
    page,
    request,
  }) => {
    const suffix = `notif-page-${uniqueSuffix()}`;
    const baseTimestamp = Date.now();
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Notification page target ${suffix}`,
      description: 'Target for an older retained notification',
    });

    const targetTitle = `Old target ${suffix}`;
    const targetResponse = await request.post('/api/notifications', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        type: 'info',
        title: targetTitle,
        message: 'This notification is older than the default inbox page',
        action_url: `/workspaces/${workspace.id}/items/${item.id}`,
        timestamp: new Date(baseTimestamp - 60_000).toISOString(),
      },
    });
    expect(targetResponse.ok(), await targetResponse.text()).toBeTruthy();

    for (let index = 0; index < 50; index += 1) {
      const response = await request.post('/api/notifications', {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
        data: {
          type: 'info',
          title: `Newer notification ${index} ${suffix}`,
          message: 'This notification belongs to the first default page',
          action_url: '/notifications',
          timestamp: new Date(baseTimestamp + index * 1_000).toISOString(),
        },
      });
      expect(response.ok(), await response.text()).toBeTruthy();
    }

    const initialInbox = page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        response.url().includes('/api/notifications') &&
        response.status() === 200
    );
    await page.goto('/');
    await expect(page.locator('#global-create-button')).toBeVisible({ timeout: 15_000 });
    await initialInbox;

    const markItemRead = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().includes('/api/notifications/mark-item-read')
    );
    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    await expect(page.getByTestId('comments-section')).toBeVisible({ timeout: 15_000 });
    expect((await markItemRead).status()).toBe(200);

    const inbox: Array<{ title: string; read: boolean }> = [];
    let offset = 0;
    while (true) {
      const inboxResponse = await request.get(`/api/notifications?limit=100&offset=${offset}`, {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
      });
      expect(inboxResponse.ok(), await inboxResponse.text()).toBeTruthy();
      const pageNotifications = (await inboxResponse.json()) as Array<{
        title: string;
        read: boolean;
      }>;
      inbox.push(...pageNotifications);
      if (pageNotifications.length < 100) break;
      offset += pageNotifications.length;
    }
    expect(
      inbox.find((notification: { title: string }) => notification.title === targetTitle)?.read
    ).toBe(true);
  });

  test('mention notification surfaces as an info toast', async ({ browser, request }) => {
    test.setTimeout(90_000);
    const { context, page } = await openToastPage(browser, request);
    try {
      const initialInbox = page.waitForResponse(
        (response) => response.url().includes('/api/notifications') && response.status() === 200
      );
      // The toast is driven by the SSE invalidation stream. Create the
      // notification only after the stream is connected so the change event is
      // delivered instead of falling into the snapshot/subscription gap.
      const streamConnected = page.waitForResponse((response) =>
        response.url().includes('/api/notifications/events')
      );
      await page.goto('/');
      // MainApp is mounted when the global create button exists — this is also
      // the moment startNotificationPoller() has been called.
      await expect(page.locator('#global-create-button')).toBeVisible({ timeout: 15_000 });
      await initialInbox;
      await streamConnected;

      const title = `E2E Mention ${uniqueSuffix()}`;
      const message = 'Agent @admin mentioned you in a comment';

      // Self-create a mention notification. The backend forces user_id = caller,
      // so this appears in this user's inbox; the frontend poller picks it up
      // and the toast allowlist (`mention`) triggers a toast.
      const resp = await context.request.post('/api/notifications', {
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

      // The toast appears from the pushed invalidation.
      const toast = page.locator('[data-testid="toast"]').filter({ hasText: title });
      await expect(toast).toBeVisible({ timeout: POLL_WAIT_MS });
      await expect(toast).toHaveAttribute('data-toast-variant', 'info');
      await expect(toast).toContainText(message);
    } finally {
      await context.close();
    }
  });

  test('assignment notification also toasts; plain "info" type does not', async ({
    browser,
    request,
  }) => {
    test.setTimeout(90_000);
    const { context, page } = await openToastPage(browser, request);
    try {
      const initialInbox = page.waitForResponse(
        (response) => response.url().includes('/api/notifications') && response.status() === 200
      );
      // Wait for the SSE stream before creating notifications so the toast
      // invalidation is not lost in the snapshot/subscription gap.
      const streamConnected = page.waitForResponse((response) =>
        response.url().includes('/api/notifications/events')
      );
      await page.goto('/');
      await expect(page.locator('#global-create-button')).toBeVisible({ timeout: 15_000 });
      await initialInbox;
      await streamConnected;

      const assignTitle = `E2E Assigned ${uniqueSuffix()}`;
      const infoTitle = `E2E Info ${uniqueSuffix()}`;

      // Assignment should toast (allowlisted).
      await context.request.post('/api/notifications', {
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
      await context.request.post('/api/notifications', {
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
    } finally {
      await context.close();
    }
  });

  test('open item pulls in new comments from another author and shows the badge', async ({
    page,
    request,
    playwright,
  }) => {
    test.setTimeout(120_000);

    // Unique suffix so retries don't collide on user/workspace unique constraints.
    const suffix = `notif-live-${uniqueSuffix()}`;

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
    const userB = generateUser(`commenter-${uniqueSuffix()}`);
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
      // Comment composer present => Comments.svelte mounted.
      // Comments section is rendered by Comments.svelte (ProseMirror placeholders
      // aren't real HTML `placeholder` attrs, so target the section wrapper).
      await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({
        timeout: 15_000,
      });

      // No comments yet.
      await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(0);

      // Post a comment as user B from their own session.
      const content = `Hi from agent B ${uniqueSuffix()}`;
      const commentResp = await userBCtx.post(`/api/v2/items/${itemId}/comments`, {
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

      // The pushed change shows the comment inline — no reload.
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

    const suffix = `notif-self-${uniqueSuffix()}`;
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

    // Admin posts a comment on their own item.

    const commentResponse = await request.post(`/api/v2/items/${itemId}/comments`, {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        content: 'My own note',
        is_private: false,
      },
    });
    expect(commentResponse.ok(), await commentResponse.text()).toBeTruthy();

    // The comment still appears from the pushed change…
    await expect(page.locator('[data-testid="comment-item"]')).toHaveCount(1, {
      timeout: POLL_WAIT_MS,
    });
    // …but the badge must not show, since the author is the current user.
    await expect(page.locator('[data-testid="new-comments-badge"]')).toHaveCount(0);
  });
});
