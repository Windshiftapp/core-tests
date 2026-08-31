import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test('navigates and drills into the same hierarchy across tree, map, and roadmap', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Hierarchy views ${stamp}`,
    key: `HV${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright collection hierarchy coverage',
  });
  const parent = await createItemViaAPI(request, workspace.id, {
    title: `Parent initiative ${stamp}`,
  });
  const child = await createItemViaAPI(request, workspace.id, {
    title: `Child delivery ${stamp}`,
    parent_id: parent.id,
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Hierarchy collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
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

  const collectionBase = `/workspaces/${workspace.id}/collections/${collection.id}`;

  await page.goto(`${collectionBase}/tree`);
  await expect(page.getByTestId('tree-view')).toBeVisible();
  await page.getByTestId(`tree-item-${parent.id}`).click();
  await expect(page).toHaveURL(new RegExp(`${collectionBase}/items/${parent.id}$`));
  await expect(page.getByTestId('item-title-edit')).toHaveText(parent.title);

  await page.goto(`${collectionBase}/map`);
  await expect(page.getByTestId('map-view')).toBeVisible();
  await expect(page.getByTestId(`map-backbone-item-${parent.id}`)).toHaveText(parent.title);
  await page.getByTestId(`map-drill-down-${parent.id}`).click();
  await expect(page).toHaveURL(new RegExp(`[?&]parent=${parent.id}(?:&|$)`));
  await expect(page.getByTestId(`map-backbone-item-${child.id}`)).toHaveText(child.title);

  await page.goto(`${collectionBase}/roadmap`);
  await expect(page.getByTestId('roadmap-view')).toBeVisible();
  await page.getByTestId(`roadmap-item-${child.id}`).click();
  await expect(page.getByTestId('item-title-edit')).toHaveText(child.title);
});
