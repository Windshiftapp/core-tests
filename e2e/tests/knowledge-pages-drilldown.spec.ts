import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Drilldown sidebar E2E coverage.
 *
 * Locks down the new UX where /pages swaps the workspace sidebar for a
 * pages-focused nav with a `+` button that creates + focuses an
 * Untitled root page, and a per-page `...` kebab menu.
 *
 * The happy-path spec (knowledge-pages.spec.ts) covers the full create →
 * edit → save → archive arc against the same updated page object. This
 * spec focuses on the drilldown chrome and the focus-on-create behavior
 * that didn't exist before.
 */
test.describe('Knowledge Pages — drilldown sidebar', () => {
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('pages-drilldown');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('entering /pages swaps the workspace sidebar for a pages drilldown', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);

    // The pages sidebar identifies itself and exposes the workspace-level
    // `+` action. The old back-arrow control was removed from the product.
    await expect(page.locator('[data-testid="pages-nav-sidebar"]')).toBeVisible();
    await expect(knowledge.addButton).toBeVisible();
  });

  test('+ creates an Untitled page, navigates to it, and focuses the title input', async ({
    page,
  }) => {
    await knowledge.gotoIndex(workspaceId);

    const createResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        /\/api\/workspaces\/\d+\/pages$/.test(res.url()) &&
        res.ok()
    );
    await knowledge.addButton.click();
    const created = (await (await createResponse).json()) as { id: number; title: string };

    await page.waitForURL(new RegExp(`/workspaces/${workspaceId}/pages/${created.id}\\b`), {
      timeout: 10000,
    });

    // Server stores a placeholder title — the user is meant to type
    // immediately because the input is focused.
    expect(created.title).toBe('Untitled');
    await expect(knowledge.titleInput).toBeFocused({ timeout: 5000 });

    // Typing replaces the placeholder; autosave fires on a debounce
    // and the tree picks up the new title via pagesTreeRefresh.bump().
    await page.keyboard.type('Welcome');
    await knowledge.waitForAutosave(workspaceId, created.id);
    await expect(knowledge.titleInput).toHaveValue('Welcome', { timeout: 5000 });
    await expect(knowledge.treeItem(created.id)).toContainText('Welcome', {
      timeout: 5000,
    });
  });

  test('+ with a page selected still creates a root page', async ({ page }) => {
    // The sidebar header `+` is a workspace-level create; it must always
    // post parent_id=null even when the user is currently viewing a page.
    // Child creation lives on the row/kebab "Add child" action instead.
    await knowledge.gotoIndex(workspaceId);
    await knowledge.createRootPage(workspaceId, 'Header plus root parent');

    const createResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        /\/api\/workspaces\/\d+\/pages$/.test(res.url()) &&
        res.ok()
    );
    await knowledge.addButton.click();
    const resp = await createResponse;
    const postBody = JSON.parse(resp.request().postData() || '{}');
    expect(postBody.parent_id ?? null).toBeNull();

    const created = (await resp.json()) as { id: number };
    await page.waitForURL(new RegExp(`/workspaces/${workspaceId}/pages/${created.id}\\b`), {
      timeout: 10000,
    });
    // Rendered at root depth (no extra left padding from depth indentation).
    const style = await knowledge.treeItem(created.id).getAttribute('style');
    expect(style ?? '').not.toMatch(/padding-left:\s*1\.3rem/);
  });

  test('right-pane ... menu opens the Move dialog', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const destinationId = await knowledge.createRootPage(workspaceId, 'Drilldown destination');
    const moverId = await knowledge.createRootPage(workspaceId, 'Drilldown mover');

    await knowledge.selectPage(moverId);
    await knowledge.openMoveDialog();
    await knowledge.pickMoveCandidate(destinationId, 'Drilldown destination');
    await knowledge.confirmMove(workspaceId, moverId);

    await page.reload();
    await page.waitForLoadState('networkidle');

    await expect(knowledge.treeItem(moverId)).toHaveAttribute('style', /padding-left:\s*1\.75rem/);
  });
});
