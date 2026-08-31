import { createCollectionViaAPI, createItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test('maps a personal task to board endpoints and opens its dedicated modal', async ({
  page,
  request,
}) => {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const personalWorkspaceResponse = await request.get('/api/workspaces/personal', {
    headers: SEC_FETCH,
  });
  expect(
    personalWorkspaceResponse.ok(),
    `load personal workspace failed (${personalWorkspaceResponse.status()})`
  ).toBeTruthy();
  const personalWorkspace = await personalWorkspaceResponse.json();
  const item = await createItemViaAPI(request, personalWorkspace.id, {
    title: `Personal board task ${stamp}`,
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Personal board ${stamp}`,
    ql_query: `title ~ "${stamp}"`,
  });
  const configurationResponse = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: SEC_FETCH,
      data: {
        columns: [
          {
            name: 'Board start',
            display_order: 0,
            color: '#6b7280',
            status_ids: [2],
          },
          {
            name: 'Board end',
            display_order: 1,
            color: '#16a34a',
            status_ids: [1],
          },
        ],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(
    configurationResponse.ok(),
    `create board configuration failed (${configurationResponse.status()})`
  ).toBeTruthy();

  await page.goto(`/collections/${collection.id}/board`);
  await expect(page.getByTestId('board-view')).toBeVisible();
  const startColumn = page.locator('#board-column-status-2');
  const endColumn = page.locator('#board-column-status-1');
  await expect(startColumn.getByTestId(`board-item-${item.id}`)).toBeVisible();
  await expect(endColumn.getByTestId(`board-item-${item.id}`)).toHaveCount(0);

  await page.getByTestId(`board-item-${item.id}`).click();
  await expect(page.getByTestId('personal-task-detail')).toBeVisible();
  await expect(page.getByTestId('item-detail')).toHaveCount(0);
  await expect(page.getByTestId('personal-task-due-date')).toBeVisible();

  const transitionResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith(`/api/items/${item.id}/transition`)
  );
  await page.getByTestId('personal-task-status-toggle').click();
  expect((await transitionResponse).ok()).toBeTruthy();

  await page.keyboard.press('Escape');
  await expect(page.getByTestId('item-detail')).toHaveCount(0);
  await expect(endColumn.getByTestId(`board-item-${item.id}`)).toBeVisible();
});
