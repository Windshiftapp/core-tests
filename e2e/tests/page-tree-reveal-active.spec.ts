import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Auto-reveal of the active page in the tree — navigating to a page
 * (deep link, search hit, in-page link) must expand every collapsed
 * ancestor so the highlighted row is actually visible in the sidebar.
 *
 *   1. Deep-linking to a nested page expands its whole ancestor chain.
 *   2. The reveal is one-shot: manually collapsing an ancestor while
 *      staying on the page is respected, not fought.
 *
 * Workspace-isolated + stateful inside the file, like the other knowledge
 * specs. retries=0 because re-running against the already-mutated
 * workspace would behave differently than the first run.
 */
test.describe('Page tree — reveal active page', () => {
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
    const data = generateWorkspace('page-tree-reveal');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('deep link to a nested page expands its ancestor chain', async ({ page }) => {
    // Three-level chain: Parent → Child → Grandchild. Each createChildPage
    // nests under the currently open page.
    parentId = await knowledge.createRootPage(workspaceId, 'Parent');
    childId = await knowledge.createChildPage(workspaceId, 'Child');
    grandchildId = await knowledge.createChildPage(workspaceId, 'Grandchild');

    // Collapse everything, then deep-link straight to the grandchild.
    await page.locator('[data-testid="pages-collapse-all"]').click();
    await expect(knowledge.treeItem(grandchildId)).toBeHidden();

    await knowledge.gotoPage(workspaceId, grandchildId);

    // The full ancestor chain is expanded and the target row is visible
    // and highlighted.
    await expect(knowledge.treeItem(grandchildId)).toBeVisible();
    await expect(knowledge.treeItem(parentId)).toHaveAttribute('data-expanded', 'true');
    await expect(knowledge.treeItem(childId)).toHaveAttribute('data-expanded', 'true');
    await expect(knowledge.treeItem(grandchildId)).toHaveClass(/active/);
  });

  test('manual collapse while on the page is respected, not re-expanded', async ({ page }) => {
    await knowledge.gotoPage(workspaceId, grandchildId);
    await expect(knowledge.treeItem(grandchildId)).toBeVisible();

    // Collapse the top of the chain while the grandchild stays active.
    await knowledge.treeItem(parentId).locator('[data-testid="page-tree-chevron"]').click();

    // The reveal effect must not fight the user: the subtree stays closed.
    await expect(knowledge.treeItem(grandchildId)).toBeHidden();
    await expect(knowledge.treeItem(parentId)).toHaveAttribute('data-expanded', 'false');
  });
});
