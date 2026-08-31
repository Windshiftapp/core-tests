import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';

test.describe('Collection editor QL URL state', () => {
  test('restores the raw query when returning to the editor with browser history', async ({
    page,
    request,
  }) => {
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`ql-url-${stamp}`));
    const item = await createItemViaAPI(
      request,
      workspace.id,
      generateItem(workspace.id, `ql-url-${stamp}`)
    );
    const collection = await createCollectionViaAPI(request, {
      name: `QL URL ${stamp}`,
      ql_query: '',
    });
    const rawQuery = `title ~ "${item.title}"`;

    await page.goto(`/collections/${collection.id}?raw=${encodeURIComponent(rawQuery)}`);
    await page.goto('/collections');
    await page.goBack();

    await expect(page).toHaveURL(new RegExp(`/collections/${collection.id}\\?raw=`));
    await expect(page.getByTestId('ql-query-summary')).toContainText(rawQuery);
    await expect(page.getByTestId(`collection-result-${item.id}`)).toBeVisible({
      timeout: 15_000,
    });
  });
});
