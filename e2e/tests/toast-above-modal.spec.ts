import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Regression (WI-316): the toast container rendered inside MainApp at z-50
 * while Modal portals itself to <body> at the same z-50, so an open
 * item-detail modal (later in the DOM) painted over the toasts — the
 * "copied item key" card was invisible on the board. The container now
 * portals to <body> at z-[110], above every dialog tier.
 *
 * Visibility assertions alone can't catch this: a buried element still
 * reports toBeVisible(). The real assertion is a hit test — the toast must
 * be the topmost element at its own center while the modal is open.
 */

// navigator.clipboard.writeText rejects without this and handleCopyKey
// swallows the error without showing the toast.
test.use({ permissions: ['clipboard-write'] });

test.describe('toasts stack above the item-detail modal', () => {
  test('copy-key toast is topmost while the modal is open on the board', async ({ page }) => {
    const workspacePage = new WorkspacePage(page);
    const itemPage = new ItemPage(page);
    const boardPage = new BoardPage(page);

    const ws = generateWorkspace();
    await workspacePage.createWorkspace(ws);
    const workspaceId = await workspacePage.getWorkspaceId(ws.name);

    const item = generateItem(0, 'toast-z');
    await itemPage.createItem(workspaceId, { title: item.title });

    await boardPage.goto(workspaceId);
    await boardPage.verifyCardExists(item.title);
    await boardPage.clickCard(item.title);

    const detail = page.getByTestId('item-detail-ready');
    await expect(detail).toBeVisible({ timeout: 10000 });

    await page.getByTestId('item-copy-key').click();

    const toast = page.getByTestId('toast');
    await expect(toast).toBeVisible({ timeout: 5000 });
    // The modal must still be open — closing it would make the hit test
    // pass trivially.
    await expect(detail).toBeVisible();

    // Clicking the toast at its center uses Playwright's real hit-target
    // checks. If the modal paints above it, Playwright rejects the click as
    // intercepted, which is the user-visible failure this regression guards.
    await toast.click();
  });
});
