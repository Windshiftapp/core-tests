import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Regression: Escape inside the comment editor on the item-detail modal
 * was being preventDefault'd by Comments.svelte (only blurring the editor),
 * so Modal.svelte's Escape-to-close handler bailed early on
 * `e.defaultPrevented` and the modal stayed open. Removing that branch in
 * Comments.svelte lets Escape bubble up and close the modal as expected.
 */

test.describe('item-detail modal — Escape inside comment editor', () => {
  test('closes the modal when Escape is pressed after typing a comment', async ({ page }) => {
    const workspacePage = new WorkspacePage(page);
    const itemPage = new ItemPage(page);
    const boardPage = new BoardPage(page);

    const ws = generateWorkspace();
    await workspacePage.createWorkspace(ws);
    const workspaceId = await workspacePage.getWorkspaceId(ws.name);

    const item = generateItem(0, 'esc-cmt');
    await itemPage.createItem(workspaceId, { title: item.title });

    await boardPage.goto(workspaceId);
    await boardPage.verifyCardExists(item.title);
    await boardPage.clickCard(item.title);

    const detail = page.locator('[role="dialog"]');
    await expect(detail).toBeVisible({ timeout: 10000 });
    await expect(detail).toContainText(item.title);

    // Comments is the default tab in ItemDetailTabs; the section is already
    // mounted with the MilkdownEditor for the new-comment form.
    const commentsSection = detail.locator('[data-testid="comments-section"]');
    await expect(commentsSection).toBeVisible({ timeout: 10000 });

    // The new-comment editor is the last ProseMirror in the section (read-only
    // ones are used for rendered existing comments, but there are none yet).
    const editor = commentsSection.locator('.ProseMirror').last();
    await editor.click();
    await page.keyboard.insertText('half-written comment');

    await page.keyboard.press('Escape');

    await expect(detail).toBeHidden({ timeout: 5000 });
  });
});
