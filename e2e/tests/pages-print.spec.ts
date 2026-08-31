import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Print / save-to-PDF route coverage.
 *
 * The page kebab exposes a "Print" item that opens a dedicated,
 * chrome-free route (`/workspaces/:id/pages/:pageId/print`) in a new tab.
 * That route renders the page title + read-only body with no app shell and
 * carries an @media print stylesheet (smart page breaks + hidden controls).
 * It auto-fires window.print() once content settles — stubbed here so the
 * (headless) print dialog never blocks the run.
 */
test.describe('Knowledge Pages — print view', () => {
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let pageId: number;
  const title = 'Print Me';
  const heading = 'Section One';
  const body = 'A paragraph that should render in the print view.';

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('pages-print');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);

    const knowledge = new KnowledgePage(page);
    pageId = await knowledge.createRootPage(workspaceId, title);
    // Seed real markdown via the API (Milkdown input rules don't fire on
    // keyboard.insertText) so the print body has a heading + paragraph.
    const res = await page.request.put(`/api/workspaces/${workspaceId}/pages/${pageId}`, {
      data: { title, content: `# ${heading}\n\n${body}\n` },
    });
    expect(res.ok()).toBeTruthy();
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    // Auto-print would otherwise pop the browser print dialog; neutralize it.
    await page.context().addInitScript(() => {
      // @ts-expect-error override for test
      window.print = () => {};
    });
  });

  test('print route renders the page chrome-free with title + body', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/pages/${pageId}/print`);

    // Standalone view: print container present, app shell absent.
    await expect(page.locator('.print-root')).toBeVisible();
    await expect(page.locator('.print-title')).toHaveText(title);
    await expect(page.locator('[data-testid="pages-nav-sidebar"]')).toHaveCount(0);
    await expect(page.locator('#pages-add-button')).toHaveCount(0);

    // Read-only body renders the seeded markdown.
    const printBody = page.locator('[data-testid="page-print-body"] .ProseMirror');
    await expect(printBody).toContainText(heading, { timeout: 15000 });
    await expect(printBody).toContainText(body);

    // Print controls are visible on screen…
    await expect(page.locator('[data-testid="page-print-button"]')).toBeVisible();

    // …and hidden under print media (so they don't appear on the sheet).
    await page.emulateMedia({ media: 'print' });
    await expect(page.locator('[data-testid="page-print-button"]')).toBeHidden();
    await page.emulateMedia({ media: 'screen' });
  });

  test('kebab "Print" opens the print route in a new tab', async ({ page }) => {
    const knowledge = new KnowledgePage(page);
    await knowledge.gotoPage(workspaceId, pageId);

    const popupPromise = page.waitForEvent('popup');
    await knowledge.toolbarKebab.click();
    await page.locator('[data-menu-item]', { hasText: 'Print' }).first().click();

    const popup = await popupPromise;
    await expect
      .poll(() => popup.url())
      .toMatch(new RegExp(`/workspaces/${workspaceId}/pages/${pageId}/print$`));
    await popup.close();
  });
});
