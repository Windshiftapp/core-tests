import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test('creates, persists, administers, and deletes an item recurrence rule', async ({
  page,
  request,
  allowConsoleError,
}) => {
  // An item without a rule is represented by a 404 from the recurrence probe.
  allowConsoleError(/\/api\/items\/\d+\/recurrence/);
  allowConsoleError(/\/api\/logbook\//);

  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Recurrence workflow ${stamp}`,
    key: `RW${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright recurrence lifecycle coverage',
  });
  const item = await createItemViaAPI(request, workspace.id, {
    title: `Recurring review ${stamp}`,
  });

  await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);

  await page.getByTestId('item-detail-actions-menu').click();
  await page.getByTestId('item-recurrence-add').click();
  await expect(page.getByTestId('recurrence-editor')).toBeVisible();

  await page.locator('#recurrence-frequency').click();
  await page.locator('#recurrence-frequency-option-DAILY').click();
  await page.locator('#recurrence-interval').fill('2');
  await expect(page.getByTestId('recurrence-editor-summary')).toContainText('2');

  const createResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/recurrence`)
  );
  await page.getByTestId('dialog-confirm').click();
  expect((await createResponse).ok()).toBeTruthy();

  await expect(page.getByTestId('item-recurrence-summary')).toBeVisible();
  await expect(page.getByTestId('recurrence-editor')).toHaveCount(0);

  await page.reload();
  await expect(page.getByTestId('item-recurrence-summary')).toBeVisible();

  await page.goto(`/workspaces/${workspace.id}/settings/recurrence`);
  const ruleRow = page.getByTestId(`recurrence-rule-row-${item.id}`);
  await expect(ruleRow).toContainText(item.title);
  await ruleRow.getByTestId('recurrence-rule-open').click();

  await expect(page.getByTestId('recurrence-detail')).toBeVisible();
  await expect(page.locator('#recurrence-interval')).toHaveValue('2');
  await page.getByTestId('recurrence-instances-tab').click();
  await expect(page.getByTestId('recurrence-instances-panel')).toBeVisible();
  await page.getByTestId('recurrence-settings-tab').click();
  await expect(page.getByTestId('recurrence-editor')).toBeVisible();
  await page.locator('#recurrence-interval').fill('3');
  await expect(page.getByTestId('recurrence-editor-summary')).toContainText('3');

  const updateResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'PUT' &&
      new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/recurrence`)
  );
  await page.getByTestId('recurrence-editor-save').click();
  expect((await updateResponse).ok()).toBeTruthy();

  await page.reload();
  const persistedRow = page.getByTestId(`recurrence-rule-row-${item.id}`);
  await expect(persistedRow).toBeVisible();
  await persistedRow.getByTestId('recurrence-rule-open').click();
  await expect(page.locator('#recurrence-interval')).toHaveValue('3');

  await page.getByTestId('recurrence-detail-back').click();
  const deletableRow = page.getByTestId(`recurrence-rule-row-${item.id}`);
  await expect(deletableRow).toBeVisible();
  await deletableRow.getByTestId(`recurrence-rule-actions-${item.id}`).click();
  await page.getByTestId('recurrence-rule-delete').click();

  const deleteResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'DELETE' &&
      new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/recurrence`)
  );
  await page.getByTestId('dialog-confirm').click();
  expect((await deleteResponse).ok()).toBeTruthy();
  await expect(deletableRow).toHaveCount(0);
});
