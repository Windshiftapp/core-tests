import { createItemViaAPI, createWorkspaceViaAPI, updateItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

/**
 * End-to-end proof of the item-detail live-update stream (WI-484): a change made
 * server-side is pushed to an already-open detail view over SSE, with no manual
 * trigger and well before the 30s adaptive poller would fire. An assertion that
 * resolves in a few seconds can only be satisfied by the SSE push — the poll
 * cadence (30s) and idle cadence (5m) are far slower, and nothing else (chat
 * bus, notification click) is exercised here.
 */
test.describe('Item detail live updates via SSE (WI-484)', () => {
  test('a field change pushes to an open detail without polling', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    allowConsoleError(/\/api\/logbook\//);
    // Item-detail load always probes /api/items/:id/recurrence; a 404 means
    // "no recurrence rule", the normal case.
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
    const ws = await createWorkspaceViaAPI(request, {
      name: `sse-live-${stamp}`,
      key: `SSE${stamp.toString().slice(-5)}`.toUpperCase(),
      description: 'item live-update SSE e2e',
    });

    const originalTitle = `Original ${stamp}`;
    const updatedTitle = `Pushed via SSE ${stamp}`;
    const item = await createItemViaAPI(request, ws.id, { title: originalTitle });

    const streamReady = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/items/${item.id}/events`) && response.status() === 200,
      { timeout: 15_000 }
    );
    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    await streamReady;
    await expect(page.getByTestId('item-detail')).toHaveAttribute(
      'data-live-updates',
      'connected',
      { timeout: 15_000 }
    );
    const title = page.getByTestId('item-title-edit');
    await expect(title).toHaveText(originalTitle);

    // Mutate server-side via the same update path the API/CLI uses. The publish
    // fires after commit → SSE hub → the open detail's stream → refreshCurrentItem.
    await updateItemViaAPI(request, item.id, { title: updatedTitle });

    // Pushed, not polled: resolves far inside the 30s poll window.
    await expect(title).toHaveText(updatedTitle, { timeout: 8000 });
  });

  test('a comment added elsewhere appears live in the open detail', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
    const ws = await createWorkspaceViaAPI(request, {
      name: `sse-comment-${stamp}`,
      key: `SSC${stamp.toString().slice(-5)}`.toUpperCase(),
      description: 'comment live-update SSE e2e',
    });
    const item = await createItemViaAPI(request, ws.id, { title: `Commented ${stamp}` });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    await expect(page.getByTestId('item-title-edit')).toHaveText(`Commented ${stamp}`);
    await expect(page.getByTestId('item-detail')).toHaveAttribute(
      'data-live-updates',
      'connected',
      { timeout: 15_000 }
    );

    const commentText = `Live comment ${stamp}`;
    const resp = await request.post(`/api/items/${item.id}/comments`, {
      data: { content: commentText },
    });
    expect(resp.ok()).toBeTruthy();

    // Pushed live (or reconciled when the stream connects), well inside the 30s
    // poll window.
    await expect(page.getByText(commentText)).toBeVisible({ timeout: 10000 });
  });

  test('deleting the item elsewhere closes the open detail', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);
    // The detail may refetch and 404 as the delete lands — that's the path
    // under test, not a failure. These cover the raw resource 404s (URL-based)
    // plus the app-level load errors the workspace store and the SCM-links
    // section log when their in-flight fetch races the delete (message-based,
    // since Chromium reports the JS bundle URL as the location, not the API path).
    allowConsoleError(/\/api\/items\/\d+/);
    allowConsoleError(/Failed to load item or workspace/);
    allowConsoleError(/Failed to load SCM links/);

    const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
    const ws = await createWorkspaceViaAPI(request, {
      name: `sse-delete-${stamp}`,
      key: `SSD${stamp.toString().slice(-5)}`.toUpperCase(),
      description: 'delete live-update SSE e2e',
    });
    const title = `Doomed ${stamp}`;
    const item = await createItemViaAPI(request, ws.id, { title });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    const titleBtn = page.getByTestId('item-title-edit');
    await expect(titleBtn).toBeVisible();
    await expect(page.getByTestId('item-detail')).toHaveAttribute(
      'data-live-updates',
      'connected',
      { timeout: 15_000 }
    );

    const resp = await request.delete(`/api/items/${item.id}`);
    expect(resp.ok()).toBeTruthy();

    // The open detail must not keep showing the deleted item: the `deleted`
    // event closes/navigates the view instead of leaving stale data.
    await expect(titleBtn).toBeHidden({ timeout: 10000 });
  });
});
