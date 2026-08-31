import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { WorkspacePage } from '../pages/workspace.page';

test.describe('Workspace Tests navigation', () => {
  let workspaceId: string;

  test.beforeEach(async ({ page }) => {
    const workspacePage = new WorkspacePage(page);
    const workspace = generateWorkspace('tests-nav');
    await workspacePage.createWorkspace(workspace);
    workspaceId = await workspacePage.getWorkspaceId(workspace.name);
    await page.goto(`/workspaces/${workspaceId}`);
  });

  test('can collapse and expand the Tests links', async ({ page }) => {
    const toggle = page.getByTestId('workspace-tests-toggle');
    const navigation = page.getByTestId('workspace-tests-navigation');

    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(navigation).toBeVisible();

    await toggle.click();

    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(navigation).toHaveCount(0);

    await toggle.click();

    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(navigation).toBeVisible();
  });
});
