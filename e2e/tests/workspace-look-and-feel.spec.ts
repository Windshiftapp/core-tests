import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

test.describe('Workspace look and feel', () => {
  test('extends a workspace gradient across an overflowing board', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace('board-gradient-overflow')
    );
    const layoutResponse = await request.put(`/api/workspaces/${workspace.id}/homepage/layout`, {
      data: {
        gradient: 3,
        applyToAllViews: true,
        sections: [],
      },
    });
    expect(layoutResponse.ok()).toBeTruthy();

    await page.setViewportSize({ width: 700, height: 800 });
    await page.goto(`/workspaces/${workspace.id}/board`);

    const background = page.getByTestId('collection-board-background');
    const board = page.getByTestId('board-view');
    await expect(background).toHaveCSS('background-image', /linear-gradient/);
    await expect(board).toBeVisible();

    const bounds = await page.evaluate(() => {
      const backgroundElement = document.querySelector(
        '[data-testid="collection-board-background"]'
      );
      const boardElement = document.querySelector('[data-testid="board-view"]');
      if (!(backgroundElement instanceof HTMLElement) || !(boardElement instanceof HTMLElement)) {
        throw new Error('Board background or content was not rendered');
      }
      return {
        backgroundRight: backgroundElement.getBoundingClientRect().right,
        boardRight: boardElement.getBoundingClientRect().right,
        viewportWidth: window.innerWidth,
      };
    });

    expect(bounds.boardRight).toBeGreaterThan(bounds.viewportWidth);
    expect(bounds.backgroundRight).toBeGreaterThanOrEqual(bounds.boardRight);
  });

  test('persists the selected workspace gradient across reload', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace('look-and-feel'));

    await page.goto(`/workspaces/${workspace.id}/look-and-feel`);
    const view = page.getByTestId('workspace-look-and-feel');
    const none = page.getByTestId('gradient-swatch-0');
    const gradient = page.getByTestId('gradient-swatch-3');

    await expect(view).toBeVisible();
    await expect(none).toHaveAttribute('aria-pressed', 'true');
    await expect(gradient).toHaveAttribute('aria-pressed', 'false');

    const saveResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        response.url().endsWith(`/api/workspaces/${workspace.id}/homepage/layout`) &&
        response.ok()
    );
    await gradient.click();
    await saveResponse;

    await expect(gradient).toHaveAttribute('aria-pressed', 'true');
    await expect(none).toHaveAttribute('aria-pressed', 'false');
    await expect(view).toHaveCSS('background-image', /linear-gradient/);

    await page.reload();
    await expect(view).toBeVisible();
    await expect(gradient).toHaveAttribute('aria-pressed', 'true');
    await expect(none).toHaveAttribute('aria-pressed', 'false');
    await expect(view).toHaveCSS('background-image', /linear-gradient/);
  });
});
