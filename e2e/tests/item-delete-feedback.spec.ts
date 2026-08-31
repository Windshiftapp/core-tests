import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test.describe('item deletion feedback', () => {
  test('shows informational feedback and allows another board card to open', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
    const workspace = await createWorkspaceViaAPI(request, {
      name: `delete-feedback-${stamp}`,
      key: `DF${stamp.slice(-6)}`.toUpperCase(),
      description: 'item deletion feedback e2e',
    });
    const deletedItem = await createItemViaAPI(request, workspace.id, {
      title: `Delete me ${stamp}`,
    });
    const remainingItem = await createItemViaAPI(request, workspace.id, {
      title: `Open me next ${stamp}`,
    });

    await page.goto(`/workspaces/${workspace.id}/board`);
    await page.getByTestId(`board-item-${deletedItem.id}`).click();
    await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });

    await page.getByTestId('item-detail-actions-menu').click();
    await page.getByTestId('item-delete-open').click();
    const deleteDialog = page.getByTestId('delete-item-dialog');
    await expect(deleteDialog).toBeVisible();
    await page.locator('#item-delete-confirm').click();
    await expect(deleteDialog).toBeHidden({ timeout: 10_000 });

    const deletionToast = page.getByTestId('toast').first();
    await expect(deletionToast).toContainText('This item was deleted.');
    await expect.soft(deletionToast).toHaveAttribute('data-toast-variant', 'info');

    await page.getByTestId('workspace-nav-board').click();
    await expect(page.getByTestId('board-view')).toBeVisible();
    await page.getByTestId(`board-item-${remainingItem.id}`).click();
    await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('item-title-edit')).toHaveText(remainingItem.title);
  });
});
