import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('comment pagination (WI-840)', () => {
  test('loads older pages in both sort orders', async ({ page, request }) => {
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(request, {
      name: `comment-pagination-${stamp}`,
      key: `CP${stamp.toString().slice(-6)}`.toUpperCase(),
      description: 'WI-840 comment pagination',
    });
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Paginated comments ${stamp}`,
    });

    for (let index = 1; index <= 30; index++) {
      const response = await request.post(`/api/items/${item.id}/comments`, {
        headers: SEC_FETCH,
        data: { content: `pagination-comment-${index}` },
      });
      expect(response.ok()).toBeTruthy();
      await response.dispose();
    }

    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    const section = page.getByTestId('comments-section');
    const rows = section.getByTestId('comment-item');
    const loadMore = section.getByTestId('comments-load-more');
    await expect(rows).toHaveCount(25);
    await expect(loadMore).toBeVisible();

    await expect(rows.first()).toContainText('pagination-comment-6');
    await expect(rows.last()).toContainText('pagination-comment-30');

    await loadMore.click();
    await expect(rows).toHaveCount(30);
    await expect(loadMore).toBeHidden();
    await expect(rows.first()).toContainText('pagination-comment-1');
    await expect(rows.last()).toContainText('pagination-comment-30');

    await page.reload();
    await expect(rows).toHaveCount(25);
    await section.getByTestId('comments-sort-toggle').click();
    await expect(rows.first()).toContainText('pagination-comment-30');
    await expect(rows.last()).toContainText('pagination-comment-6');

    await section.getByTestId('comments-load-more').click();
    await expect(rows).toHaveCount(30);
    await expect(rows.first()).toContainText('pagination-comment-30');
    await expect(rows.last()).toContainText('pagination-comment-1');
  });
});
