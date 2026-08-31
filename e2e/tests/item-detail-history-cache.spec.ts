import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test('keeps item detail ready through a back-forward cache lifecycle', async ({
  page,
  request,
  allowConsoleError,
}, testInfo) => {
  allowConsoleError(/\/api\/logbook\//);
  allowConsoleError(/\/api\/items\/\d+\/recurrence/);

  const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
  const workspace = await createWorkspaceViaAPI(request, {
    name: `item-history-${stamp}`,
    key: `IH${stamp.slice(-6)}`.toUpperCase(),
    description: 'item detail history cache lifecycle e2e',
  });
  const item = await createItemViaAPI(request, workspace.id, {
    title: `History item ${stamp}`,
  });

  await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
  const detail = page.getByTestId('item-detail-ready');
  await expect(detail).toBeVisible();
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);

  await page.evaluate(async () => {
    window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }));
    window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
    await Promise.resolve();
  });

  await expect(detail).toBeVisible();
  await expect(page.getByTestId('item-title-edit')).toHaveText(item.title);
});
