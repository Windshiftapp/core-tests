import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

test.describe('Page canvas width', () => {
  let workspaceId: string;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('page-canvas-width');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test('uses a centered readable width until the user expands it', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 });
    const knowledge = new KnowledgePage(page);
    await knowledge.createRootPage(workspaceId, 'Readable canvas');

    const pane = page.getByTestId('pages-view');
    const canvas = page.getByTestId('page-canvas');
    const widthToggle = page.getByTestId('page-canvas-width-toggle');

    await expect(canvas).toHaveAttribute('data-width', 'comfortable');
    await expect(widthToggle).toHaveAttribute('aria-pressed', 'false');

    const [paneBox, comfortableBox] = await Promise.all([pane.boundingBox(), canvas.boundingBox()]);
    if (!paneBox || !comfortableBox) {
      throw new Error('page canvas dimensions were unavailable');
    }
    expect(comfortableBox.width).toBeGreaterThanOrEqual(paneBox.width * 0.75 - 2);
    expect(
      Math.abs(comfortableBox.x - paneBox.x - (paneBox.width - comfortableBox.width) / 2)
    ).toBeLessThanOrEqual(2);

    await widthToggle.click();

    await expect(canvas).toHaveAttribute('data-width', 'wide');
    await expect(widthToggle).toHaveAttribute('aria-pressed', 'true');
    const wideBox = await canvas.boundingBox();
    if (!wideBox) {
      throw new Error('wide page canvas dimensions were unavailable');
    }
    expect(wideBox.width).toBeGreaterThan(comfortableBox.width + 100);
  });
});
