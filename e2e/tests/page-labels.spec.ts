import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Page labels (workspace-scoped, attach to pages only) E2E coverage.
 *
 * Walks the user journey:
 *   1. Create a page-label inline from the picker on the page header.
 *   2. See the chip appear on the page.
 *   3. Filter the sidebar by label — only labeled pages (and their
 *      ancestors) remain visible.
 *   4. Detach the label from the page; the chip + sidebar match disappear.
 *
 * Page-label CRUD is completely separate from work-item labels — this spec
 * deliberately does not touch the items system.
 */
test.describe('Page labels — workspace-scoped, attach to pages', () => {
  // Tests share state inside a single workspace (later tests rely on
  // pages/labels created earlier). retries=0 because re-running a stateful
  // test against the already-mutated workspace would behave differently
  // than the first run and surface false-positive failures.
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;
  let labeledChildId: number;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('page-labels');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('create label via picker, attach to page, see chip', async ({ page }) => {
    const pageId = await knowledge.createRootPage(workspaceId, 'Design notes');

    // Open the page-label picker on the page header.
    await page.locator('[data-testid="page-label-picker-trigger"]').first().click();
    const picker = page.locator('[data-testid="page-label-picker"]');
    await expect(picker).toBeVisible();

    // Type the label name. On the first run the inline create row appears
    // (workspace has no labels yet); on a Playwright auto-retry the label
    // already exists, so we click the existing row instead — both branches
    // result in the same final state: label attached to this page.
    await picker.locator('[data-testid="page-label-picker-search"]').fill('design');
    const createBtn = picker.locator('[data-testid="page-label-picker-create"]');
    const existingRow = picker
      .locator('[data-testid="page-label-picker-row"]')
      .filter({ hasText: 'design' });
    // Whichever branch runs, the label ends up attached via a POST.
    // Arm the waiter before clicking; the attach request is often fast
    // enough that waiting after the click races and misses it.
    const attachResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}/labels`) &&
        res.ok(),
      { timeout: 5000 }
    );
    if (await createBtn.isVisible()) {
      await createBtn.click();
    } else {
      await existingRow.click();
    }
    await attachResponse;

    const chip = page.locator(`[data-testid="page-label-chip"]`).first();
    await expect(chip).toBeVisible();
    await expect(chip).toContainText('design');
  });

  test('sidebar filter narrows tree to labeled pages and ancestors', async ({ page }) => {
    // Seed two more pages — one labeled, one not.
    const parent = await knowledge.createRootPage(workspaceId, 'Parent');
    const labeled = await knowledge.createChildPage(workspaceId, 'Labeled child');
    labeledChildId = labeled;

    // Attach the existing "design" label to the child via the picker.
    await page.locator('[data-testid="page-label-picker-trigger"]').first().click();
    const picker = page.locator('[data-testid="page-label-picker"]');
    await expect(picker).toBeVisible();
    const childAttachResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${labeled}/labels`) &&
        res.ok(),
      { timeout: 5000 }
    );
    await picker
      .locator('[data-testid="page-label-picker-row"]')
      .filter({ hasText: 'design' })
      .click();
    await childAttachResponse;
    // Close the popover so the sidebar trigger isn't shadowed.
    await page.keyboard.press('Escape');

    // Also create a sibling root page that is NOT labeled — it should be
    // hidden once the filter is on.
    const unlabeled = await knowledge.createRootPage(workspaceId, 'Untagged');

    // Open the sidebar filter and pick "design".
    await page.locator('[data-testid="pages-filter-trigger"]').click();
    const filterPicker = page.locator('[data-testid="page-label-picker"]');
    await expect(filterPicker).toBeVisible();
    await filterPicker
      .locator('[data-testid="page-label-picker-row"]')
      .filter({ hasText: 'design' })
      .click();
    await page.keyboard.press('Escape');

    // Active filter chip is rendered above the tree.
    await expect(page.locator('[data-testid="pages-filter-chip"]')).toBeVisible();

    // Labeled page is visible; its parent (kept for context) is visible too.
    // The untagged sibling is hidden via `.tree-item.hidden { display: none }`.
    await expect(knowledge.treeItem(labeled)).toBeVisible();
    await expect(knowledge.treeItem(parent)).toBeVisible();
    await expect(knowledge.treeItem(unlabeled)).toBeHidden();

    // Clear the filter — every tree row is back.
    await page.locator('[data-testid="pages-filter-clear"]').click();
    await expect(knowledge.treeItem(unlabeled)).toBeVisible();
  });

  test('detach label removes the chip + drops the sidebar match', async ({ page }) => {
    // Navigate to /pages first so the tree is rendered regardless of where
    // the previous test left the URL.
    await knowledge.gotoIndex(workspaceId);
    // Use the "Labeled child" page seeded by the previous test; selection
    // by clicking the tree row puts the page into PagesView.
    const target = knowledge.treeItem(labeledChildId);
    await target.locator('.page-button').click();
    await knowledge.titleInput.waitFor({ state: 'visible', timeout: 5000 });

    // Remove the chip via the × on the chip itself.
    const chip = page.locator('[data-testid="page-label-chip"]').first();
    await expect(chip).toBeVisible();
    const pageId = knowledge.getCurrentPageId();
    const detachResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'DELETE' &&
        /\/api\/workspaces\/\d+\/pages\/\d+\/labels\/\d+$/.test(res.url()) &&
        res.ok(),
      { timeout: 5000 }
    );
    await chip.locator('[data-testid="page-label-chip-remove"]').click();
    await detachResponse;

    // Chip is gone for this page.
    await expect(page.locator('[data-testid="page-label-chip"]')).toHaveCount(0);

    // Re-applying the sidebar filter for "design" now hides "Labeled child"
    // (we just detached it). "Design notes" from the first test still has
    // the label attached, so the filter narrows the tree but doesn't empty
    // it — the specific "Labeled child" row going hidden is the proof that
    // the detach landed end-to-end.
    await page.locator('[data-testid="pages-filter-trigger"]').click();
    await page
      .locator('[data-testid="page-label-picker"]')
      .locator('[data-testid="page-label-picker-row"]')
      .filter({ hasText: 'design' })
      .click();
    await page.keyboard.press('Escape');

    await expect(knowledge.treeItem(labeledChildId)).toBeHidden();

    // Cleanup so the next test doesn't inherit the filter.
    await page.locator('[data-testid="pages-filter-clear"]').click();
    // Touch pageId so the linter doesn't complain about an unused capture.
    expect(pageId).toBeGreaterThan(0);
  });
});
