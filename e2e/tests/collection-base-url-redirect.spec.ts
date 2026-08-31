import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

/**
 * `/workspaces/:id/collections/:collectionId` maps to the `workspace-detail`
 * view, which redirects to the workspace's configured default view. The
 * collection has to survive that redirect — dropping it lands on the workspace
 * board, which reads the workspace board configuration (columns, card fields)
 * instead of the collection's, with no error and no visual hint (WI-893).
 */
test('collection base URL keeps its collection through the default-view redirect', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Collection redirect ${stamp}`,
    key: `CR${stamp.toString().slice(-6)}`.toUpperCase(),
  });
  const inCollection = await createItemViaAPI(request, workspace.id, {
    title: `In collection ${stamp}`,
  });
  const outsideCollection = await createItemViaAPI(request, workspace.id, {
    title: `Outside ${stamp}`,
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Redirect collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "In collection ${stamp}"`,
  });
  // Give the collection its own board configuration — the point of the
  // redirect is that this config is the one that gets loaded.
  const configuration = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configuration.ok()).toBeTruthy();

  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}`);

  await expect(page).toHaveURL(
    new RegExp(`/workspaces/${workspace.id}/collections/${collection.id}/[a-z]+$`)
  );

  // The landed-on view is scoped to the collection, not the whole workspace.
  await expect(page.getByTestId(`board-item-${inCollection.id}`)).toBeVisible();
  await expect(page.getByTestId(`board-item-${outsideCollection.id}`)).toHaveCount(0);
});
