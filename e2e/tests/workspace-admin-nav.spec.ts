import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { WorkspacePage } from '../pages/workspace.page';
import { WorkspaceSettingsPage } from '../pages/workspace-settings.page';

/**
 * Workspace admin navigation (folded sidebar).
 *
 * The workspace admin area no longer uses horizontal tabs — clicking into
 * Settings swaps the workspace sidebar for a folded admin nav (back link +
 * one link per module), and each module renders as a standard page with a
 * PageHeader. These tests pin that behavior.
 */

// label → { module route segment, page-header heading }
const MODULES: Array<{ id: string; heading: string }> = [
  { id: 'general', heading: 'General' },
  { id: 'categories', heading: 'Categories' },
  { id: 'members', heading: 'Members' },
  { id: 'configuration', heading: 'Configuration Sets' },
  { id: 'source-control', heading: 'Source Control' },
  { id: 'issue-sync', heading: 'Issue Sync' },
  { id: 'recurrence', heading: 'Recurrence' },
  { id: 'danger', heading: 'Remove Workspace' },
];

test.describe('Workspace admin folded sidebar', () => {
  let settingsPage: WorkspaceSettingsPage;
  let workspacePage: WorkspacePage;
  let workspaceId: string;

  test.beforeEach(async ({ page }) => {
    settingsPage = new WorkspaceSettingsPage(page);
    workspacePage = new WorkspacePage(page);
    const ws = generateWorkspace();
    await workspacePage.createWorkspace(ws);
    workspaceId = await workspacePage.getWorkspaceId(ws.name);
  });

  test('swaps the sidebar for the admin nav with a back link and every module', async ({
    page,
  }) => {
    await settingsPage.goto(workspaceId);

    // Folded admin sidebar + back link present; old horizontal tablist gone.
    await expect(page.locator(settingsPage.adminNav)).toBeVisible({ timeout: 5000 });
    await expect(page.locator(settingsPage.backLink)).toBeVisible();
    await expect(page.locator('[role="tablist"]')).toHaveCount(0);

    // One nav link per module.
    for (const m of MODULES) {
      await expect(page.locator(`[data-testid="workspace-admin-nav-${m.id}"]`)).toBeVisible();
    }
  });

  test('each module routes to its own page with a header', async ({ page }) => {
    await settingsPage.goto(workspaceId);

    for (const m of MODULES) {
      await page.locator(`[data-testid="workspace-admin-nav-${m.id}"]`).click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}/settings/${m.id}$`));
      await expect(page.getByRole('heading', { level: 1, name: m.heading })).toBeVisible({
        timeout: 5000,
      });
    }
  });

  test('back link returns to the workspace and restores the normal sidebar', async ({ page }) => {
    await settingsPage.goto(workspaceId);
    await expect(page.locator(settingsPage.adminNav)).toBeVisible({ timeout: 5000 });

    await page.locator(settingsPage.backLink).click();
    // The workspace root redirects to its default view (e.g. /board), so just
    // assert we left the settings area and the admin nav is gone.
    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}(/(?!settings)[^/]*)?$`));
    await expect(page.locator(settingsPage.adminNav)).toHaveCount(0);
  });

  test('collapsed sidebar shows the module icons and a back arrow', async ({ page }) => {
    // Seed the collapsed state before the app mounts.
    await page.addInitScript(() => {
      localStorage.setItem('windshift-ws-sidebar-collapsed', 'true');
    });
    await settingsPage.goto(workspaceId);

    // Back arrow (to the workspace) + an icon link per module, by href.
    await expect(page.locator(`a[href="/workspaces/${workspaceId}"]`).first()).toBeVisible({
      timeout: 5000,
    });
    for (const m of MODULES) {
      await expect(
        page.locator(`a[href="/workspaces/${workspaceId}/settings/${m.id}"]`)
      ).toBeVisible();
    }
  });
});
