import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  expect,
  type Locator,
  type Page,
  test,
} from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';

/**
 * Board "Sort by" control with Bubble Mode.
 *
 * Rank mode (default) keeps the manual frac_index order. Bubble Mode floats
 * the most-recently-active cards to the top of each column (driven by the
 * items.last_active_at column, which a comment bumps). This spec pins:
 *   - the mode toggle + its persistence across reload, and
 *   - that commenting on an older card bubbles it to the top in Bubble Mode.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

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

async function createItem(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  statusId: number
): Promise<number> {
  const resp = await request.post(`${BASE_URL}/api/items`, {
    headers: SEC_FETCH,
    data: { workspace_id: workspaceId, title, status_id: statusId },
  });
  expect(resp.ok(), `create item failed (${resp.status()})`).toBeTruthy();
  const body = await resp.json();
  return (body.data ?? body).id as number;
}

async function addComment(
  request: APIRequestContext,
  itemId: number,
  content: string
): Promise<void> {
  const resp = await request.post(`${BASE_URL}/api/items/${itemId}/comments`, {
    headers: SEC_FETCH,
    data: { content, is_private: false },
  });
  expect(resp.ok(), `add comment failed (${resp.status()})`).toBeTruthy();
}

async function dragCardToCard(page: Page, card: Locator, target: Locator): Promise<void> {
  const cardBox = await card.boundingBox();
  const targetBox = await target.boundingBox();
  if (!cardBox || !targetBox) {
    throw new Error('Board drag source and target must both be visible');
  }

  const startX = cardBox.x + cardBox.width / 2;
  const startY = cardBox.y + cardBox.height / 2;
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(startX + 12, startY + 12);
  await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + 12, { steps: 12 });
  await page.mouse.up();
}

test.describe('Board Bubble Mode sort', () => {
  test('toggles, persists, and bubbles a commented card to the top', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace());
    const workspaceId: number = workspace.id;

    const statuses = await listWorkspaceStatuses(request, workspaceId);
    const openStatus = statuses.find((s) => s.name === 'Open') ?? statuses[0];

    // Create four Open items in order; created sequentially so each has a
    // distinct, increasing created_at (which seeds last_active_at).
    const ids: number[] = [];
    for (let i = 1; i <= 4; i++) {
      ids.push(await createItem(request, workspaceId, `Item ${i}`, openStatus.id));
    }
    const [oldest, , , newest] = ids;

    const boardPage = new BoardPage(page);
    await boardPage.goto(String(workspaceId));

    const openColumn = page.locator(`[data-status-column][data-status-id="${openStatus.id}"]`);
    await expect(openColumn.locator('.board-card')).toHaveCount(4);

    const firstCardId = async (): Promise<string | null> =>
      openColumn.locator('.board-card').first().getAttribute('data-item-id');

    const sortTrigger = page.locator('[data-testid="board-sort-by-menu"]');

    // Rank mode (default): backend frac_index order — first created is first.
    await expect(sortTrigger).toContainText('Sort by');
    await expect(await firstCardId()).toBe(String(oldest));

    // Switch to Bubble Mode: newest-active rises to the top. With no activity
    // yet, last_active_at == created_at, so the most recently created is first.
    await sortTrigger.click();
    await page.locator('[data-testid="board-sort-bubble"]').click();
    await expect(sortTrigger).toContainText('Sort: Bubble');
    await expect.poll(firstCardId).toBe(String(newest));

    // Bubble Mode owns the card order, so a same-column drag cannot reorder
    // cards. The board must explain this rather than silently ignoring it.
    const oldestCard = page.getByTestId(`board-item-${oldest}`);
    await expect(oldestCard).toHaveAttribute('draggable', 'true');
    await dragCardToCard(page, oldestCard, page.getByTestId(`board-item-${newest}`));
    await expect(page.getByTestId('toast')).toHaveAttribute('data-toast-variant', 'info');
    await expect(page.getByTestId('toast-message-info')).toHaveText(
      'Switch to Rank mode to arrange cards manually.'
    );

    // Comment on the oldest item, then reload. Bubble Mode persists (localStorage)
    // and the freshly fetched last_active_at floats the commented card to the top.
    await addComment(request, oldest, 'Bumping the oldest item');
    await page.reload();
    await page.waitForLoadState('networkidle');

    await expect(sortTrigger).toContainText('Sort: Bubble');
    await expect(openColumn.locator('.board-card')).toHaveCount(4);
    await expect.poll(firstCardId).toBe(String(oldest));
  });
});
