import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

test.describe('Page tree drag-and-drop reparenting', () => {
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('page-tree-drag-reparent');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test('reparents through the row middle while edge drops keep sibling ordering', async ({
    page,
  }) => {
    const knowledge = new KnowledgePage(page);
    const firstId = await knowledge.createRootPage(workspaceId, 'First root');
    const secondId = await knowledge.createRootPage(workspaceId, 'Second root');
    const thirdId = await knowledge.createRootPage(workspaceId, 'Third root');
    await knowledge.gotoIndex(workspaceId);

    const first = knowledge.treeItem(firstId);
    const second = knowledge.treeItem(secondId);
    const third = knowledge.treeItem(thirdId);

    const secondBox = await second.boundingBox();
    if (!secondBox) throw new Error('Second page row has no bounding box');

    const reparented = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/workspaces/${workspaceId}/pages/${firstId}/move`) &&
        response.ok()
    );
    await first.dragTo(second, {
      targetPosition: { x: secondBox.width / 2, y: secondBox.height / 2 },
    });
    await reparented;

    await expect(second).toHaveAttribute('data-expanded', 'true');
    await second.getByTestId('page-tree-chevron').click();
    await expect(first).toBeHidden();

    const thirdBox = await third.boundingBox();
    const collapsedSecondBox = await second.boundingBox();
    if (!thirdBox || !collapsedSecondBox) {
      throw new Error('Page rows must remain visible before the dwell drag');
    }

    const reordered = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/workspaces/${workspaceId}/pages/${thirdId}/move`) &&
        response.ok()
    );
    await page.mouse.move(thirdBox.x + thirdBox.width / 2, thirdBox.y + thirdBox.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      collapsedSecondBox.x + collapsedSecondBox.width / 2,
      collapsedSecondBox.y + collapsedSecondBox.height / 2,
      { steps: 8 }
    );

    await expect(second).toHaveAttribute('data-expanded', 'true', {
      timeout: 1500,
    });

    const expandedSecondBox = await second.boundingBox();
    if (!expandedSecondBox) throw new Error('Expanded page row has no bounding box');
    await page.mouse.move(
      expandedSecondBox.x + expandedSecondBox.width / 2,
      expandedSecondBox.y + 1,
      { steps: 4 }
    );
    await page.mouse.up();
    await reordered;

    const reorderedThirdBox = await third.boundingBox();
    const reorderedSecondBox = await second.boundingBox();
    if (!reorderedThirdBox || !reorderedSecondBox) {
      throw new Error('Reordered page rows have no bounding box');
    }
    expect(reorderedThirdBox.y).toBeLessThan(reorderedSecondBox.y);
  });
});
