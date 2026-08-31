import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Page revision-history drawer E2E coverage.
 *
 * Walks the user journey:
 *   1. Create a page through the UI (revision 1=create, revision 2=title
 *      autosave from Untitled to the requested title).
 *   2. Edit the body three times via the same setContentViaAPI path the
 *      editor's autosave uses (revisions 3, 4, 5).
 *   3. Open the History drawer from the page toolbar's meatball menu.
 *   4. Assert five revision rows are listed, newest-first.
 *   5. Expand revision #4 (the kale-clause version) and click Restore.
 *   6. Confirm the dialog; the drawer fires onRestored which reloads the
 *      page.
 *   7. Assert a sixth row exists with change_type=restore.
 */
test.describe('Page revision history — drawer + restore', () => {
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;

  const v1Body = '# Bogus Spec\n\nFirst body.';
  const v2Body = '# Bogus Spec\n\nWith the kale clause.';
  const v3Body = '# Bogus Spec\n\nFinal — no kale.';

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('pages-history');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('drawer lists revisions newest-first and restore creates a new revision', async ({
    page,
  }) => {
    // Seed five revisions: create + UI title autosave + three content edits.
    const pageId = await knowledge.createRootPage(workspaceId, 'Bogus Spec');
    await knowledge.setContentViaAPI(workspaceId, pageId, 'Bogus Spec', v1Body);
    await knowledge.setContentViaAPI(workspaceId, pageId, 'Bogus Spec', v2Body);
    await knowledge.setContentViaAPI(workspaceId, pageId, 'Bogus Spec', v3Body);

    // Open the drawer via the meatball menu.
    await page.locator('[data-testid="page-toolbar-kebab"]').click();
    await page.locator('[data-testid="page-menu-history"]').click();

    const drawer = page.locator('[data-testid="pages-history-drawer"]');
    await expect(drawer).toBeVisible();

    // Five revisions: 1=create + title autosave + three edits (v1, v2, v3), newest first.
    const rows = drawer.locator('[data-testid="pages-history-row"]');
    await expect(rows).toHaveCount(5);
    await expect(rows.nth(0)).toHaveAttribute('data-revision', '5');
    await expect(rows.nth(4)).toHaveAttribute('data-revision', '1');

    // Expand revision #4 (the kale-clause body) and restore it.
    const kaleRow = drawer.locator('[data-testid="pages-history-row"][data-revision="4"]');
    await kaleRow.locator('.rev-header').click();
    await kaleRow.locator('[data-testid="pages-history-restore"]').click();

    // Confirm the restore in the global confirm dialog. Use the stable
    // test id because the button's accessible name includes the keyboard
    // hint ("Restore ↵"). Arm the page-reload waiter before confirming.
    const restoredPageReload = page.waitForResponse(
      (res) =>
        res.request().method() === 'GET' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}`) &&
        res.ok(),
      { timeout: 10000 }
    );
    await page.locator('[data-testid="dialog-confirm"]').click();

    // Drawer's onRestored fires loadPage, which refetches the body via
    // GET /api/.../pages/{id}. Wait for the editor to reflect v2.
    await restoredPageReload;

    // A sixth revision (change_type=restore) is now present. The drawer
    // self-refetches after a successful restore.
    await expect(rows).toHaveCount(6, { timeout: 5000 });
    await expect(rows.nth(0)).toHaveAttribute('data-revision', '6');

    // Page body must reflect the restored revision (v2 = the kale clause),
    // not the final v3 body. Without this, a regression that creates a
    // revision row but never updates the page content would still pass.
    // Read the body from the API since the editor renders Markdown into
    // ProseMirror nodes that don't expose the raw source as plain text.
    const fetched = await page.request.get(`/api/workspaces/${workspaceId}/pages/${pageId}`);
    expect(fetched.ok()).toBeTruthy();
    const fetchedBody = await fetched.json();
    expect(fetchedBody.content).toContain('With the kale clause');
    expect(fetchedBody.content).not.toContain('Final — no kale');
  });
});
