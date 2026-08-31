import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test('moves a work item to another workspace from its detail view', async ({ page, request }) => {
  const stamp = Date.now();
  const suffix = stamp.toString().slice(-6);
  const source = await createWorkspaceViaAPI(request, {
    name: `Move source ${stamp}`,
    key: `MS${suffix}`,
    description: 'Source workspace for the cross-workspace move test',
  });
  const destination = await createWorkspaceViaAPI(request, {
    name: `Move destination ${stamp}`,
    key: `MD${suffix}`,
    description: 'Destination workspace for the cross-workspace move test',
  });
  const item = await createItemViaAPI(request, source.id, {
    title: `Move me ${stamp}`,
  });
  const oldKey = `${source.key}-${item.workspace_item_number}`;

  await page.goto(`/workspaces/${source.id}/items/${item.id}`);
  await expect(page.getByTestId('item-detail-ready')).toBeVisible();
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);

  await page.getByTestId('item-detail-actions-menu').click();
  await page.getByTestId('item-move-workspace-open').click();
  await page.locator('#item-move-workspace-picker').click();

  const previewResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/move-workspace/preview`)
  );
  await page.getByTestId(`item-move-workspace-option-${destination.id}`).click();
  expect((await previewResponse).ok()).toBeTruthy();

  const preview = page.getByTestId('item-move-workspace-preview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText(oldKey);
  await expect(page.getByTestId('item-move-workspace-item-type')).toBeEnabled();
  await expect(page.getByTestId('item-move-workspace-status')).toBeEnabled();

  const moveResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/move-workspace`)
  );
  await page.getByTestId('item-move-workspace-confirm').click();
  const response = await moveResponse;
  expect(response.ok()).toBeTruthy();
  const result = await response.json();
  expect(result.old_key).toBe(oldKey);
  expect(result.new_key).toMatch(new RegExp(`^${destination.key}-\\d+$`));

  await expect(page).toHaveURL(new RegExp(`/workspaces/${destination.id}/items/${item.id}$`));
  await expect(page.getByTestId('item-detail-ready')).toBeVisible();
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);

  await page.reload();
  await expect(page.getByTestId('item-detail-ready')).toBeVisible();
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);
});
