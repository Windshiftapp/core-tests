import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

test.describe('Workspace breadcrumbs', () => {
  let workspace: { id: number; name: string };

  test.beforeEach(async ({ request }, testInfo) => {
    workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`breadcrumbs-${testInfo.testId.slice(-6)}`)
    );
  });

  test.afterEach(async ({ request }) => {
    const response = await request.delete(`/api/v2/workspaces/${workspace.id}`);
    expect(response.status()).toBe(204);
  });

  test('dashboard opens the standard workspace selector and enters the selected workspace', async ({
    page,
  }) => {
    await page.goto('/');
    await expect(page.getByTestId('workspace-breadcrumbs')).toBeVisible();
    await expect(page.getByTestId('workspace-breadcrumb-current')).toHaveCount(0);
    await page.getByTestId('workspace-breadcrumb-picker').click();
    await page.getByTestId('workspaces-search').fill(workspace.name);
    await page.getByTestId('workspace-dropdown-item').click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspace.id}/board$`));
    await expect(page.getByTestId('workspace-breadcrumb-current')).toHaveText(workspace.name);
    await expect(page.getByTestId('board-view')).toBeVisible();
    await page.goto('/teams');
    await expect(page).toHaveURL(/\/teams$/);
    await page.goto(`/workspaces/${workspace.id}/board`);
    await expect(page.getByTestId('board-view')).toBeVisible();
  });

  test('workspace views retain the breadcrumb above the sidebar and page', async ({
    page,
  }, testInfo) => {
    await page.goto(`/workspaces/${workspace.id}/board`);
    const breadcrumbs = page.getByTestId('workspace-breadcrumbs');
    const current = page.getByTestId('workspace-breadcrumb-current');
    await expect(current).toHaveText(workspace.name);
    await expect(current).toHaveAttribute('href', `/workspaces/${workspace.id}`);
    await expect(page.getByTestId('workspace-navigation-scroll')).not.toContainText(workspace.name);
    await page.getByTestId('workspace-nav-list').click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspace.id}/list$`));
    await expect(current).toHaveText(workspace.name);
    await expect(breadcrumbs).toBeVisible();
    await current.click();
    await expect(page.getByTestId('board-view')).toBeVisible();
    await page.reload();
    await expect(current).toHaveText(workspace.name);
    const breadcrumbBox = await breadcrumbs.boundingBox();
    const boardBox = await page.getByTestId('board-view').boundingBox();
    if (!breadcrumbBox || !boardBox) throw new Error('Breadcrumb or board has no layout box');
    expect(boardBox.y).toBeGreaterThanOrEqual(breadcrumbBox.y + breadcrumbBox.height);
    await page.screenshot({ path: testInfo.outputPath('workspace-breadcrumbs.png') });
  });

  test('switches workspace from the breadcrumb on a narrow screen', async ({ page, request }) => {
    const other = await createWorkspaceViaAPI(request, generateWorkspace('breadcrumb-switch'));
    try {
      await page.setViewportSize({ width: 720, height: 900 });
      await page.goto(`/workspaces/${workspace.id}/board`);
      await expect(page.getByTestId('workspace-breadcrumb-current')).toHaveText(workspace.name);
      await page.getByTestId('workspace-breadcrumb-picker').click();
      await page.getByTestId('workspaces-search').fill(other.name);
      await page.getByTestId('workspace-dropdown-item').click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${other.id}/board$`));
      await expect(page.getByTestId('workspace-breadcrumb-current')).toHaveText(other.name);
      await page.getByTestId('mobile-workspace-nav-trigger').click();
      await page.getByTestId('workspace-nav-list').click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${other.id}/list$`));
      await expect(page.getByTestId('workspace-breadcrumb-current')).toBeVisible();
    } finally {
      const response = await request.delete(`/api/v2/workspaces/${other.id}`);
      expect(response.status()).toBe(204);
    }
  });
});
