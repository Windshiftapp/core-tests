import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, type Page, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

async function resultHeaders(page: Page) {
  return page
    .getByTestId('collection-search-results-table')
    .locator('th')
    .allTextContents()
    .then((headers) => headers.map((header) => header.trim()));
}

test.describe('Collection search list columns', () => {
  test('reuses list columns, keeps workspace context, and persists changes', async ({
    page,
    request,
  }) => {
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`collection-search-columns-${stamp}`)
    );
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Collection search columns ${stamp}`,
    });
    const collection = await createCollectionViaAPI(request, {
      name: `Collection search columns ${stamp}`,
      ql_query: `title ~ "${item.title}"`,
    });

    await page.goto(`/collections/${collection.id}`);
    await expect(page.getByTestId(`collection-result-${item.id}`)).toBeVisible();

    await expect
      .poll(() => resultHeaders(page))
      .toEqual(
        expect.arrayContaining(['Key', 'Title', 'Workspace', 'Status', 'Priority', 'Created'])
      );

    await page.getByTestId('collection-column-selector-trigger').click();
    await page.getByTestId('remove-column-priority').click();
    const removeResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().includes(`/api/collections/${collection.id}/board-configuration`)
    );
    await page.getByTestId('column-selector-apply').click();
    await removeResponse;

    await expect.poll(() => resultHeaders(page)).not.toContain('Priority');

    await page.getByTestId('collection-column-selector-trigger').click();
    await page.getByTestId('add-column-assignee').click();
    const updateResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        response.url().includes(`/api/collections/${collection.id}/board-configuration/`)
    );
    await page.getByTestId('column-selector-apply').click();
    await updateResponse;

    await expect.poll(() => resultHeaders(page)).toContain('Assignee');

    await page.reload();
    await expect(page.getByTestId(`collection-result-${item.id}`)).toBeVisible();
    await expect.poll(() => resultHeaders(page)).toContain('Assignee');
    await expect.poll(() => resultHeaders(page)).not.toContain('Priority');
  });
});
