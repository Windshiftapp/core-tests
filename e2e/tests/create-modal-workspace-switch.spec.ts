import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * The create modal can target any workspace the user may create in, which is
 * not necessarily the workspace behind the modal.
 *
 * Regression: selecting another workspace activated it in the shared
 * workspaceDataStore, whose route-hydration effect switched the active scope
 * back. The two effects fought until Svelte threw
 * `effect_update_depth_exceeded` and the modal froze.
 */
test('switching the create-modal workspace stays stable and creates in the chosen workspace', async ({
  page,
}) => {
  const workspacePage = new WorkspacePage(page);
  const itemPage = new ItemPage(page);

  const pageErrors: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));

  const workspaceA = generateWorkspace();
  await workspacePage.createWorkspace(workspaceA);
  const idA = await workspacePage.getWorkspaceId(workspaceA.name);

  const workspaceB = generateWorkspace();
  await workspacePage.createWorkspace(workspaceB);
  const idB = await workspacePage.getWorkspaceId(workspaceB.name);

  await itemPage.gotoWorkspaceBacklog(idA);
  await page.click('#global-create-button');
  const title = page.locator('#work-item-title');
  await expect(title).toBeVisible({ timeout: 5000 });

  await page.getByTestId('create-workspace-chip').click();
  await page
    .getByTestId('create-workspace-chip-listbox')
    .getByTestId('create-workspace-chip-option')
    .filter({ hasText: workspaceB.key })
    .click();

  // The selection applies and the modal keeps working.
  await expect(page.getByTestId('create-workspace-chip')).toContainText(workspaceB.key);
  expect(
    pageErrors.filter((error) => error.includes('effect_update_depth_exceeded')),
    `effect loop errors: ${pageErrors.join('\n')}`
  ).toEqual([]);

  const itemTitle = `Switched workspace ${Date.now()}`;
  await title.fill(itemTitle);
  await page.locator('#create-modal-submit').click();
  await expect(page.getByTestId('create-modal')).toBeHidden({ timeout: 10_000 });

  // The item was created in the workspace chosen inside the modal, not the
  // one the modal was opened from.
  await itemPage.gotoWorkspaceBacklog(idB);
  await itemPage.verifyItemExists(itemTitle);

  expect(pageErrors, `page errors: ${pageErrors.join('\n')}`).toEqual([]);
});
