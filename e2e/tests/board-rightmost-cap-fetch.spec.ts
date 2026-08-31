import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';

/**
 * Board split fetch for the capped rightmost column.
 *
 * When a board configuration enables show_rightmost_column_last_50, the
 * collection store must NOT pull every completed item through the paged
 * items fetch (they used to eat the page budget even though only the
 * latest 50 ever render). Instead it issues:
 *   - the main items fetch with status_id_not=<rightmost statuses>, and
 *   - a separate fetch of the latest 50 rightmost items (status_id=...,
 *     order_by=updated_at desc) whose pagination.total feeds the
 *     "Showing latest N of M items in this column" label.
 *
 * This spec pins the request split at the network level and the rendered
 * counts at the UI level.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

const DONE_ITEMS = 55;
const OPEN_ITEMS = 5;
const UNBOUNDED_DONE_ITEMS = 105;
const UNBOUNDED_OPEN_ITEMS = 38;

async function listWorkspaceStatuses(
  request: APIRequestContext,
  workspaceId: number
): Promise<Array<{ id: number; name: string }>> {
  const resp = await request.get(`/api/workspaces/${workspaceId}/statuses`, {
    headers: SEC_FETCH,
  });
  expect(resp.ok(), `list statuses failed (${resp.status()})`).toBeTruthy();
  const body = await resp.json();
  return body.data ?? body;
}

async function createItemWithStatus(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  statusId: number,
  description = ''
) {
  const resp = await request.post(`${BASE_URL}/api/items`, {
    headers: SEC_FETCH,
    data: { workspace_id: workspaceId, title, description, status_id: statusId },
  });
  expect(resp.ok(), `create item failed (${resp.status()})`).toBeTruthy();
  const body = await resp.json();
  return body.data ?? body;
}

test.describe('Board status-partitioned fetch', () => {
  test('loads every unfinished item before paging completed work', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace());
    const workspaceId: number = workspace.id;

    const statuses = await listWorkspaceStatuses(request, workspaceId);
    const doneStatus = statuses.find((s) => s.name === 'Done') ?? statuses[statuses.length - 1];
    const openStatus = statuses.find((s) => s.name === 'Open') ?? statuses[0];
    expect(doneStatus.id).not.toBe(openStatus.id);

    const configResp = await request.post(
      `${BASE_URL}/api/collections/default/board-configuration?workspace_id=${workspaceId}`,
      {
        headers: SEC_FETCH,
        data: { columns: [], show_rightmost_column_last_50: false },
      }
    );
    expect(configResp.ok(), `create board config failed (${configResp.status()})`).toBeTruthy();

    // Creating completed items first makes the old shared 100-item page contain
    // no unfinished cards at all.
    for (let batch = 0; batch < UNBOUNDED_DONE_ITEMS; batch += 10) {
      await Promise.all(
        Array.from({ length: Math.min(10, UNBOUNDED_DONE_ITEMS - batch) }, (_, i) =>
          createItemWithStatus(
            request,
            workspaceId,
            `Paged Done item ${batch + i + 1}`,
            doneStatus.id
          )
        )
      );
    }
    for (let batch = 0; batch < UNBOUNDED_OPEN_ITEMS; batch += 10) {
      await Promise.all(
        Array.from({ length: Math.min(10, UNBOUNDED_OPEN_ITEMS - batch) }, (_, i) =>
          createItemWithStatus(
            request,
            workspaceId,
            `Always visible Open item ${batch + i + 1}`,
            openStatus.id
          )
        )
      );
    }

    const unfinishedFetch = page.waitForRequest(
      (r) => r.url().includes('/api/items?') && r.url().includes(`status_id_not=${doneStatus.id}`),
      { timeout: 15000 }
    );
    const completedFetch = page.waitForRequest(
      (r) => {
        if (!r.url().includes('/api/items?')) return false;
        const params = new URL(r.url()).searchParams;
        return (
          params.get('status_id') === String(doneStatus.id) &&
          params.get('limit') === '100' &&
          params.get('page') === '1'
        );
      },
      { timeout: 15000 }
    );

    const boardPage = new BoardPage(page);
    await boardPage.goto(String(workspaceId));
    await Promise.all([unfinishedFetch, completedFetch]);

    const openColumn = page.locator(`[data-status-column][data-status-id="${openStatus.id}"]`);
    await expect(openColumn.locator('.board-card')).toHaveCount(UNBOUNDED_OPEN_ITEMS);

    const doneColumn = page.locator(`[data-status-column][data-status-id="${doneStatus.id}"]`);
    await expect(doneColumn).toContainText(`100 of ${UNBOUNDED_DONE_ITEMS} Item`);
    await expect(doneColumn.locator('.board-card')).toHaveCount(100);

    const nextCompletedPage = page.waitForRequest(
      (r) => {
        if (!r.url().includes('/api/items?')) return false;
        const params = new URL(r.url()).searchParams;
        return params.get('status_id') === String(doneStatus.id) && params.get('page') === '2';
      },
      { timeout: 15000 }
    );
    await page.getByTestId('board-load-more').click();
    await nextCompletedPage;

    await expect(doneColumn.locator('.board-card')).toHaveCount(UNBOUNDED_DONE_ITEMS);
    await expect(openColumn.locator('.board-card')).toHaveCount(UNBOUNDED_OPEN_ITEMS);
    await expect(page.getByTestId('board-load-more')).toHaveCount(0);
  });

  test('splits the items fetch and reports server-side column totals', async ({
    page,
    request,
  }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace());
    const workspaceId: number = workspace.id;

    const statuses = await listWorkspaceStatuses(request, workspaceId);
    const doneStatus = statuses.find((s) => s.name === 'Done') ?? statuses[statuses.length - 1];
    const openStatus = statuses.find((s) => s.name === 'Open') ?? statuses[0];
    expect(doneStatus.id).not.toBe(openStatus.id);

    // Enable the rightmost-column cap with no explicit columns — the board
    // falls back to one column per status, so the rightmost column is the
    // Done status.
    const configResp = await request.post(
      `${BASE_URL}/api/collections/default/board-configuration?workspace_id=${workspaceId}`,
      {
        headers: SEC_FETCH,
        data: { columns: [], show_rightmost_column_last_50: true },
      }
    );
    expect(configResp.ok(), `create board config failed (${configResp.status()})`).toBeTruthy();

    // Seed the description-search target first so the deterministic recency
    // tie-break leaves it outside the latest-50 cap.
    const descriptionNeedle = `scoped board description ${workspaceId}`;
    const hiddenSearchTarget = await createItemWithStatus(
      request,
      workspaceId,
      'Older completed search target',
      doneStatus.id,
      descriptionNeedle
    );

    // Seed more Done items than the 50-card cap, plus a few Open ones.
    const remainingDoneItems = DONE_ITEMS - 1;
    for (let batch = 0; batch < remainingDoneItems; batch += 10) {
      await Promise.all(
        Array.from({ length: Math.min(10, remainingDoneItems - batch) }, (_, i) =>
          createItemWithStatus(request, workspaceId, `Done item ${batch + i + 1}`, doneStatus.id)
        )
      );
    }
    await Promise.all(
      Array.from({ length: OPEN_ITEMS }, (_, i) =>
        createItemWithStatus(request, workspaceId, `Open item ${i + 1}`, openStatus.id)
      )
    );

    // The split must be observable on the wire: a main fetch excluding the
    // rightmost statuses and a capped fetch for just those statuses.
    const mainFetch = page.waitForRequest(
      (r) => r.url().includes('/api/items?') && r.url().includes(`status_id_not=${doneStatus.id}`),
      { timeout: 15000 }
    );
    const capFetch = page.waitForRequest(
      (r) =>
        r.url().includes('/api/items?') &&
        new URL(r.url()).searchParams.get('status_id') === String(doneStatus.id) &&
        new URL(r.url()).searchParams.get('limit') === '50',
      { timeout: 15000 }
    );

    const boardPage = new BoardPage(page);
    await boardPage.goto(String(workspaceId));
    await Promise.all([mainFetch, capFetch]);

    // Rendered counts: 50 cards shown, server-side total in the labels.
    const doneColumn = page.locator(`[data-status-column][data-status-id="${doneStatus.id}"]`);
    await expect(doneColumn).toContainText(`50 of ${DONE_ITEMS} Item`);
    await expect(doneColumn).toContainText(
      `Showing latest 50 of ${DONE_ITEMS} items in this column.`
    );
    await expect(doneColumn.locator('.board-card')).toHaveCount(50);
    await expect(page.getByTestId(`board-item-${hiddenSearchTarget.id}`)).toHaveCount(0);

    const openColumn = page.locator(`[data-status-column][data-status-id="${openStatus.id}"]`);
    await expect(openColumn.locator('.board-card')).toHaveCount(OPEN_ITEMS);

    // Everything that can render is loaded — no Load More for hidden
    // completed items.
    await expect(page.locator('[data-testid="board-load-more"]')).toHaveCount(0);

    const scopedSearchRequest = page.waitForRequest((candidate) => {
      if (!candidate.url().includes('/api/items?')) return false;
      return new URL(candidate.url()).searchParams.get('search') === descriptionNeedle;
    });
    await page.getByTestId('board-search-input').fill(descriptionNeedle);
    const searchRequest = await scopedSearchRequest;
    const searchParams = new URL(searchRequest.url()).searchParams;
    expect(searchParams.get('workspace_id')).toBe(String(workspaceId));
    expect(searchParams.has('status_id')).toBe(false);
    expect(searchParams.has('status_id_not')).toBe(false);
    expect(searchParams.has('completed_activity_days')).toBe(false);
    await expect(page.getByTestId(`board-item-${hiddenSearchTarget.id}`)).toBeVisible();

    await page.getByTestId('board-search-input').fill('');
    await expect(page.getByTestId(`board-item-${hiddenSearchTarget.id}`)).toHaveCount(0);
    await expect(doneColumn.locator('.board-card')).toHaveCount(50);
  });
});
