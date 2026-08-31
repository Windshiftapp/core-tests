import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';

test('moves a page subtree to a selected workspace and parent', async ({ page, request }) => {
  const source = await createWorkspaceViaAPI(request, generateWorkspace('page-move-source'));
  const destination = await createWorkspaceViaAPI(
    request,
    generateWorkspace('page-move-destination')
  );

  const createPage = async (workspaceId: number, title: string, parentId: number | null = null) => {
    const response = await request.post(`/api/workspaces/${workspaceId}/pages`, {
      data: { title, content: `${title} body`, parent_id: parentId },
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json() as Promise<{ id: number }>;
  };

  const sourceRoot = await createPage(source.id, 'Portable handbook');
  const sourceChild = await createPage(source.id, 'Portable chapter', sourceRoot.id);
  const destinationParent = await createPage(destination.id, 'Destination library');

  const knowledge = new KnowledgePage(page);
  await knowledge.gotoPage(String(source.id), sourceRoot.id);
  await knowledge.openMoveDialog();

  const destinationTreeLoaded = page.waitForResponse(
    (response) =>
      response.request().method() === 'GET' &&
      response.url().endsWith(`/api/workspaces/${destination.id}/pages/tree`) &&
      response.ok()
  );
  await page.locator('#page-move-workspace-picker').click();
  await page.getByTestId(`page-move-workspace-option-${destination.id}`).click();
  await destinationTreeLoaded;

  const preview = page.getByTestId('page-move-cross-workspace-preview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText('2');

  await page.locator('#page-move-picker').click();
  await page.getByTestId(`page-move-parent-option-${destinationParent.id}`).click();

  const moveResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith(`/api/workspaces/${source.id}/pages/${sourceRoot.id}/move`) &&
      response.ok()
  );
  await page.getByTestId('page-move-confirm').click();
  await moveResponse;
  await expect(page).toHaveURL(new RegExp(`/workspaces/${source.id}/pages$`));

  await knowledge.gotoPage(String(destination.id), sourceRoot.id);
  await expect(knowledge.titleInput).toHaveValue('Portable handbook');
  await expect(knowledge.treeItem(destinationParent.id)).toBeVisible();
  await knowledge.treeItem(sourceRoot.id).getByTestId('page-tree-chevron').click();
  await expect(knowledge.treeItem(sourceChild.id)).toBeVisible();
  await expect(knowledge.treeItem(sourceChild.id)).toHaveAttribute(
    'style',
    /padding-left:\s*2\.5rem/
  );
});
