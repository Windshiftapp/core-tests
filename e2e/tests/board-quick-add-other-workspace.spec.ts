import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';

/**
 * A workspace board can create cards only in its own workspace. This keeps a
 * card from being optimistically appended to a board it does not belong to.
 */
test.describe('Board quick-add workspace scope', () => {
  test('offers only the current workspace', async ({ page, request }) => {
    const boardPage = new BoardPage(page);
    const currentWorkspace = await createWorkspaceViaAPI(request, generateWorkspace());
    const otherWorkspace = await createWorkspaceViaAPI(request, generateWorkspace());

    await boardPage.goto(String(currentWorkspace.id));
    await boardPage.verifyBoardVisible();

    const firstColumn = page.getByTestId('board-column').first();
    await firstColumn.getByTestId(/^board-column-add-/).click();

    await page.getByTestId('quick-add-workspace').click();
    await expect(
      page.getByTestId(`quick-add-workspace-option-${currentWorkspace.id}`)
    ).toBeVisible();
    await expect(page.getByTestId(`quick-add-workspace-option-${otherWorkspace.id}`)).toHaveCount(
      0
    );
  });
});
