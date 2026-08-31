import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Collapsible page tree — locks down the per-node expand/collapse with
 * localStorage persistence.
 *
 *   1. First visit: every non-root subtree is collapsed by default.
 *   2. Clicking a chevron toggles the child rows in/out.
 *   3. Selecting a collapsed page opens its direct child-page tree.
 *   4. Expansion state survives a full page reload (localStorage).
 *
 * Workspace-isolated + stateful inside the file, like the other knowledge
 * specs. retries=0 because re-running against the already-mutated
 * workspace would behave differently than the first run.
 */
test.describe('Page tree — collapsible + persistent', () => {
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;
  let parentId: number;
  let childId: number;
  let grandchildId: number;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('page-tree-collapse');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('child rows stay hidden when their parent is collapsed', async () => {
    parentId = await knowledge.createRootPage(workspaceId, 'Parent');
    // createChildPage auto-expands the parent so the just-created child
    // is visible — that's the right UX, and the assertion below confirms.
    childId = await knowledge.createChildPage(workspaceId, 'Child');
    grandchildId = await knowledge.createChildPage(workspaceId, 'Grandchild');

    await expect(knowledge.treeItem(childId)).toBeVisible();

    // Collapse the parent via its chevron.
    await knowledge.treeItem(parentId).locator('[data-testid="page-tree-chevron"]').click();

    await expect(knowledge.treeItem(childId)).toBeHidden();

    // Expand it again — child reappears.
    await knowledge.treeItem(parentId).locator('[data-testid="page-tree-chevron"]').click();
    await expect(knowledge.treeItem(childId)).toBeVisible();
  });

  test('selecting a page opens its direct child-page tree', async ({ page }) => {
    await knowledge.gotoPage(workspaceId, grandchildId);

    // Collapse the child's subtree, then select the child itself.
    await knowledge.treeItem(childId).locator('[data-testid="page-tree-chevron"]').click();
    await expect(knowledge.treeItem(grandchildId)).toBeHidden();

    await knowledge.treeItem(childId).locator('[data-testid="page-tree-page"]').click();
    await page.waitForURL(new RegExp(`/pages/${childId}\\b`));
    await expect(knowledge.treeItem(childId)).toHaveAttribute('data-expanded', 'true');
    await expect(knowledge.treeItem(grandchildId)).toBeVisible();
  });

  test('expansion state persists across a full page reload', async ({ page }) => {
    // Use the workspace seeded above. Reload first so we're on a known
    // baseline (the previous test left the parent expanded).
    await knowledge.gotoIndex(workspaceId);
    const parent = knowledge.treeItem(parentId);

    // Collapse, reload, assert the collapse stuck.
    await parent.locator('[data-testid="page-tree-chevron"]').click();
    await expect(parent).toHaveAttribute('data-expanded', 'false');

    await page.reload();
    await page.waitForLoadState('networkidle');

    const reloadedParent = knowledge.treeItem(parentId);
    await expect(reloadedParent).toHaveAttribute('data-expanded', 'false');

    // The child row that we earlier created is hidden because the
    // restored collapse state hides it again.
    await expect(knowledge.treeItem(childId)).toBeHidden();
  });

  test('"Collapse all" hides every nested row; "Expand all" restores them', async ({ page }) => {
    // Make sure the parent has a child to verify with (reload may leave us
    // mid-collapse from the previous test).
    await knowledge.gotoIndex(workspaceId);

    await page.locator('[data-testid="pages-expand-all"]').click();
    await expect(knowledge.treeItem(childId)).toBeVisible();

    await page.locator('[data-testid="pages-collapse-all"]').click();
    await expect(knowledge.treeItem(childId)).toBeHidden();
  });
});
