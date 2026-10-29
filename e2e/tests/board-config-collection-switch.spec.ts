import {
  authenticateAdminRequest,
  createCollectionViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * Switching collections from the board configuration page must keep the
 * `board/configure` path instead of collapsing the view name into an invalid
 * `board-config` segment (which rendered the 404 page).
 */
test.describe('Board configuration collection switch', () => {
  test('re-scopes board config to the selected collection without a 404', async ({
    page,
    request,
  }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-config-collection-${Date.now()}`)
    );
    const collection = await createCollectionViaAPI(request, {
      name: `Board config collection ${Date.now()}`,
      workspace_id: workspace.id,
      ql_query: '',
    });

    await page.goto(`/workspaces/${workspace.id}/board/configure`);
    await expect(page.getByTestId('board-config-save')).toBeVisible();

    await page.getByTestId('workspace-collection-select').click();
    await page.getByTestId(`workspace-collection-option-${collection.id}`).click();

    await expect(page).toHaveURL(
      `/workspaces/${workspace.id}/collections/${collection.id}/board/configure`
    );
    await expect(page.getByTestId('board-config-save')).toBeVisible();

    // Back to "All items" drops the collection segment and stays on the page.
    await page.getByTestId('workspace-collection-select').click();
    await page.getByTestId('workspace-collection-option-default').click();
    await expect(page).toHaveURL(`/workspaces/${workspace.id}/board/configure`);
    await expect(page.getByTestId('board-config-save')).toBeVisible();
  });
});
