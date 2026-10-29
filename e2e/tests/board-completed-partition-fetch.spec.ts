import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, type Page, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';

/**
 * Board split fetch for completed work.
 *
 * Completed statuses are fetched separately from unfinished items so they
 * cannot eat the page budget: the main items fetch excludes the completed
 * statuses (status_id_not=...) and a second fetch pages the completed items.
 *
 * This spec pins the request split at the network level and the rendered
 * counts at the UI level.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

const UNBOUNDED_DONE_ITEMS = 105;
const UNBOUNDED_OPEN_ITEMS = 38;

async function listWorkspaceStatuses(
  request: APIRequestContext,
  workspaceId: number
): Promise<Array<{ id: number; name: string }>> {
  const resp = await request.get(`/api/v2/workspaces/${workspaceId}/statuses`, {
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
  const resp = await request.post(`${BASE_URL}/api/v2/items`, {
    headers: SEC_FETCH,
    data: { workspace_id: workspaceId, title, description, status_id: statusId },
  });
  expect(resp.ok(), `create item failed (${resp.status()})`).toBeTruthy();
  const body = await resp.json();
  return body.data ?? body;
}

function boardColumn(page: Page, statusId: number) {
  return page
    .getByTestId('board-column')
    .filter({ has: page.getByTestId(`board-column-status-${statusId}`) });
}

test.describe('Board status-partitioned fetch', () => {
  test('loads every unfinished item before paging completed work', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace());
    const workspaceId: number = workspace.id;

    const statuses = await listWorkspaceStatuses(request, workspaceId);
    const doneStatus = statuses.find((s) => s.name === 'Done') ?? statuses[statuses.length - 1];
    const openStatus = statuses.find((s) => s.name === 'Open') ?? statuses[0];
    expect(doneStatus.id).not.toBe(openStatus.id);

    const configResp = await request.put(
      `${BASE_URL}/api/v2/workspaces/${workspaceId}/board-configuration`,
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
      (r) => r.url().includes('/api/v2/items?') && r.url().includes(`status_id_not=${doneStatus.id}`),
      { timeout: 15000 }
    );
    const completedFetch = page.waitForRequest(
      (r) => {
        if (!r.url().includes('/api/v2/items?')) return false;
        const params = new URL(r.url()).searchParams;
        return (
          params.get('status_id') === String(doneStatus.id) &&
          params.get('page_size') === '100' &&
          params.get('page') === '1'
        );
      },
      { timeout: 15000 }
    );

    const boardPage = new BoardPage(page);
    await boardPage.goto(String(workspaceId));
    await Promise.all([unfinishedFetch, completedFetch]);

    const openColumn = boardColumn(page, openStatus.id);
    await expect(openColumn.getByTestId(/^board-item-/)).toHaveCount(UNBOUNDED_OPEN_ITEMS);

    const doneColumn = boardColumn(page, doneStatus.id);
    await expect(doneColumn).toContainText(`100 of ${UNBOUNDED_DONE_ITEMS} Item`);
    await expect(doneColumn.getByTestId(/^board-item-/)).toHaveCount(100);

    const nextCompletedPage = page.waitForRequest(
      (r) => {
        if (!r.url().includes('/api/v2/items?')) return false;
        const params = new URL(r.url()).searchParams;
        return params.get('status_id') === String(doneStatus.id) && params.get('page') === '2';
      },
      { timeout: 15000 }
    );
    await page.getByTestId('board-load-more').click();
    await nextCompletedPage;

    await expect(doneColumn.getByTestId(/^board-item-/)).toHaveCount(UNBOUNDED_DONE_ITEMS);
    await expect(openColumn.getByTestId(/^board-item-/)).toHaveCount(UNBOUNDED_OPEN_ITEMS);
    await expect(page.getByTestId('board-load-more')).toHaveCount(0);
  });
});
