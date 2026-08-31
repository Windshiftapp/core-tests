import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Board View Tests
 * Tests board view columns, cards, and navigation to detail
 */

test.describe('Board View', () => {
  let boardPage: BoardPage;
  let workspacePage: WorkspacePage;
  let itemPage: ItemPage;
  let testWorkspace: ReturnType<typeof generateWorkspace>;
  let workspaceId: string;

  test.beforeEach(async ({ page }) => {
    boardPage = new BoardPage(page);
    workspacePage = new WorkspacePage(page);
    itemPage = new ItemPage(page);
    testWorkspace = generateWorkspace();

    // Create workspace and get its numeric ID
    await workspacePage.createWorkspace(testWorkspace);
    workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
  });

  test.describe('Board Display', () => {
    test('should display board view', async () => {
      await boardPage.goto(workspaceId);
      await boardPage.verifyBoardVisible();
    });

    test('should display columns', async () => {
      await boardPage.goto(workspaceId);

      const columnCount = await boardPage.getColumnCount();
      expect(columnCount).toBeGreaterThan(0);
    });

    test('should display column headers', async () => {
      await boardPage.goto(workspaceId);

      const columnNames = await boardPage.getColumnNames();
      expect(columnNames.length).toBeGreaterThan(0);
    });
  });

  test.describe('Board Cards', () => {
    test('should display item as card on board', async () => {
      const item = generateItem(0, 'board-card');

      await itemPage.createItem(workspaceId, {
        title: item.title,
        description: item.description,
      });

      await boardPage.goto(workspaceId);
      await boardPage.verifyCardExists(item.title);
    });

    test('should display multiple cards', async ({ page }) => {
      const item1 = generateItem(0, 'card1');
      const item2 = generateItem(0, 'card2');

      await itemPage.createItem(workspaceId, { title: item1.title });
      // createItem() already waits for its modal to detach, so we can start
      // the next one immediately.
      await itemPage.createItem(workspaceId, { title: item2.title });

      await boardPage.goto(workspaceId);
      await boardPage.verifyCardExists(item1.title);
      await boardPage.verifyCardExists(item2.title);
    });

    test('should navigate to item detail when clicking card', async () => {
      const item = generateItem(0, 'click-card');

      await itemPage.createItem(workspaceId, {
        title: item.title,
        description: item.description,
      });

      await boardPage.goto(workspaceId);
      await boardPage.verifyCardExists(item.title);
      await boardPage.clickCard(item.title);

      // Wait for item detail dialog to appear
      const detail = boardPage.page.locator('[role="dialog"]');
      await detail.waitFor({ state: 'visible', timeout: 10000 });
      await expect(detail).toContainText(item.title);
    });
  });
});
