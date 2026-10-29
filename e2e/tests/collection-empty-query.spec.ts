import { randomUUID } from 'node:crypto';
import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';
import { preferenceSave } from '../helpers/preferences';

test('empty collections can page past fifty items and keep items when completion filtering is disabled', async ({
  page,
  request,
  allowConsoleError,
}) => {
  // On a shared server another worker's test can delete a collection this
  // browser surface still live-reloads; the store degrades gracefully on the
  // 404 (returns null), but Chromium always logs the failed resource itself.
  allowConsoleError(
    /\/api\/v2\/collections\/\d+\/board-configuration\/bootstrap.*404/,
  );
  const stamp = randomUUID().slice(0, 8);
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Empty query ${stamp}`,
    key: `EQ${stamp}`.toUpperCase(),
  });
  let collection: Awaited<ReturnType<typeof createCollectionViaAPI>> | undefined;
  const headers = { 'Sec-Fetch-Site': 'same-origin' };
  try {
    for (let i = 0; i < 51; i++) {
      await createItemViaAPI(request, workspace.id, {
        title: `Empty query ${stamp} ${i}`,
      });
    }
    collection = await createCollectionViaAPI(request, {
      name: `Empty query ${stamp}`,
      ql_query: '',
    });
    await page.goto(`/collections/${collection.id}/list`);
    const rows = page.getByTestId(/^workspace-item-row-/);
    await expect(rows).toHaveCount(50);
    const firstPageIds = await rows.evaluateAll((elements) =>
      elements.map((element) => element.getAttribute('data-testid'))
    );
    await expect(page.getByTestId('pagination-next')).toBeEnabled();
    await page.goto(`/collections/${collection.id}`);
    const searchRows = page.getByTestId(/^collection-result-/);
    await expect(searchRows).toHaveCount(50);
    const searchFirstPageIds = await searchRows.evaluateAll((elements) =>
      elements.map((element) => element.getAttribute('data-testid'))
    );
    await page.getByTestId('pagination-next').click();
    await expect(page.getByTestId('pagination-previous')).toBeEnabled();
    await expect
      .poll(() =>
        searchRows.evaluateAll((elements) =>
          elements.map((element) => element.getAttribute('data-testid'))
        )
      )
      .not.toEqual(searchFirstPageIds);
    await expect(searchRows.first()).toBeVisible();
    await page.goto(`/collections/${collection.id}/list`);
    await expect(rows).toHaveCount(50);
    await page.getByTestId('pagination-next').click();
    await expect(page.getByTestId('pagination-previous')).toBeEnabled();
    await expect
      .poll(() =>
        rows.evaluateAll((elements) =>
          elements.map((element) => element.getAttribute('data-testid'))
        )
      )
      .not.toEqual(firstPageIds);
    await expect(rows.first()).toBeVisible();
    const toggle = page.getByTestId('collection-hide-completed');
    const saved = preferenceSave(page);
    await toggle.click();
    await saved;
    await expect(toggle).toHaveAttribute('aria-checked', 'false');
    await expect(rows).toHaveCount(50);
    await expect(page.getByTestId('pagination-previous')).toBeDisabled();
    await page.reload();
    await expect(toggle).toHaveAttribute('aria-checked', 'false');
    await expect(rows).toHaveCount(50);
    await expect(page.getByTestId('pagination-next')).toBeEnabled();
  } finally {
    if (collection) {
      const response = await request.delete(`/api/v2/collections/${collection.id}`, { headers });
      expect(response.ok(), await response.text()).toBeTruthy();
    }
    const response = await request.delete(`/api/v2/workspaces/${workspace.id}`, { headers });
    expect(response.ok(), await response.text()).toBeTruthy();
  }
});
