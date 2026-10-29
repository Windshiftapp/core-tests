import { createCollectionViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import {
  createGatedWorkspaceWithSessions,
  disposeGatedWorkspace,
  type GatedWorkspace,
} from '../fixtures/role-sessions';

/**
 * Workspace-linked collections are shared objects: a member with item.view on
 * the workspace sees and opens another user's collection, while manage actions
 * stay with the owner.
 */
test('workspace-linked collections are visible and usable for workspace members', async ({
  page,
  request,
  browser,
}) => {
  test.setTimeout(90_000);
  const suffix = `csh${Date.now()}`;
  let fixture: GatedWorkspace | undefined;
  let collectionId: number | undefined;
  try {
    fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
      { label: 'viewer', role: 'Viewer' },
    ]);
    const { workspaceId, sessionsByLabel } = fixture;
    const viewer = sessionsByLabel.viewer;

    const collection = await createCollectionViaAPI(request, {
      name: `Shared collection ${suffix}`,
      workspace_id: workspaceId,
    });
    collectionId = collection.id;

    // The owner sees manage actions.
    await page.goto('/collections/workspace');
    await expect(page.getByTestId(`collection-row-${collection.id}`)).toBeVisible();
    await page.getByTestId(`collection-actions-${collection.id}`).click();
    await expect(page.getByTestId('collection-action-edit')).toBeVisible();
    await expect(page.getByTestId('collection-action-delete')).toBeVisible();
    await page.keyboard.press('Escape');

    // The workspace member sees the collection but not manage actions.
    await viewer.page.goto('/collections/workspace');
    await expect(viewer.page.getByTestId(`collection-row-${collection.id}`)).toBeVisible();
    await viewer.page.getByTestId(`collection-actions-${collection.id}`).click();
    await expect(viewer.page.getByTestId('collection-action-view')).toBeVisible();
    await expect(viewer.page.getByTestId('collection-action-edit')).toHaveCount(0);
    await expect(viewer.page.getByTestId('collection-action-delete')).toHaveCount(0);
    await viewer.page.keyboard.press('Escape');

    // The member can open the shared collection and load its board.
    await viewer.page.getByTestId(`collection-row-${collection.id}`).click();
    await expect(viewer.page).toHaveURL(
      new RegExp(`/workspaces/${workspaceId}/collections/${collection.id}`),
    );
    await expect(viewer.page.getByTestId('board-header')).toBeVisible({ timeout: 15_000 });

    // The member cannot change collection privacy.
    await viewer.page.goto(`/workspaces/${workspaceId}/collections/${collection.id}`);
    await expect(viewer.page.getByTestId('collection-privacy-toggle')).toHaveCount(0);
  } finally {
    if (collectionId !== undefined) {
      const deleted = await request.delete(`/api/v2/collections/${collectionId}`, {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
      });
      expect(deleted.ok(), await deleted.text()).toBeTruthy();
    }
    if (fixture) await disposeGatedWorkspace(request, fixture);
  }
});

test('global collections are shared by default and can be made private', async ({
  page,
  request,
  browser,
}) => {
  test.setTimeout(90_000);
  const suffix = `cgp${Date.now()}`;
  let fixture: GatedWorkspace | undefined;
  let collectionId: number | undefined;
  try {
    fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
      { label: 'viewer', role: 'Viewer' },
    ]);
    const viewer = fixture.sessionsByLabel.viewer;

    const collection = await createCollectionViaAPI(request, {
      name: `Global collection ${suffix}`,
    });
    collectionId = collection.id;

    // Shared by default: any authenticated caller sees the global collection.
    await viewer.page.goto('/collections');
    await expect(viewer.page.getByTestId(`collection-row-${collection.id}`)).toBeVisible();

    // The owner opts out from the editor.
    await page.goto(`/collections/${collection.id}`);
    const toggle = page.getByTestId('collection-privacy-toggle');
    await expect(toggle).toHaveText(/Shared/);
    await toggle.click();
    await expect(toggle).toHaveText(/Private/);

    // The private collection is gone for the other user.
    await viewer.page.goto('/collections');
    await expect(viewer.page.getByTestId(`collection-row-${collection.id}`)).toHaveCount(0);
  } finally {
    if (collectionId !== undefined) {
      const deleted = await request.delete(`/api/v2/collections/${collectionId}`, {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
      });
      expect(deleted.ok(), await deleted.text()).toBeTruthy();
    }
    if (fixture) await disposeGatedWorkspace(request, fixture);
  }
});
