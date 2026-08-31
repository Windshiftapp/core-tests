import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';

test('creates and edits an item through the browser', {
  tag: '@critical-browser',
}, async ({ page, request }) => {
  await authenticateAdminRequest(request);
  const suffix = `critical-item-${Date.now()}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
  const item = generateItem(workspace.id, suffix);
  const itemPage = new ItemPage(page);

  // Use the minimal create form here. Rich editor composition is covered by
  // the dedicated critical comment journey in this matrix.
  await itemPage.createItem(String(workspace.id), { title: item.title });
  await itemPage.verifyItemExists(item.title);

  await itemPage.openItemDetailModal(item.title);
  const updatedTitle = `${item.title} updated`;
  await itemPage.editTitleInline(updatedTitle);
  await itemPage.closeItemDetailModal();
  await itemPage.gotoWorkspaceBacklog(String(workspace.id));
  await expect(await itemPage.findItemByTitle(updatedTitle)).toBeVisible();
});
