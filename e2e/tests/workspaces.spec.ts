import { expect, logicalPath, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Workspace Management Tests
 * Tests workspace CRUD operations using authenticated context
 */

test.describe('Workspace Management', () => {
  let workspacePage: WorkspacePage;
  let testWorkspace: ReturnType<typeof generateWorkspace>;

  test.beforeEach(async ({ page }) => {
    workspacePage = new WorkspacePage(page);
    testWorkspace = generateWorkspace();
  });

  test.describe('Create Workspace', () => {
    test('should create workspace with valid data', async () => {
      await workspacePage.createWorkspace(testWorkspace);

      // Verify workspace was created
      await workspacePage.verifyWorkspaceExists(testWorkspace.name);
    });

    test('should display workspace in list', async () => {
      // Create workspace
      await workspacePage.createWorkspace(testWorkspace);

      // Navigate to workspaces list
      await workspacePage.goto();

      // Find the workspace
      const workspace = await workspacePage.findWorkspaceByName(testWorkspace.name);
      await expect(workspace).toBeVisible();

      // Verify name is shown (key column is not displayed in the workspace list table)
      await expect(workspace).toContainText(testWorkspace.name);
    });

    test('should validate unique workspace key', async ({ page }) => {
      // Create first workspace
      await workspacePage.createWorkspace(testWorkspace);

      // Try to create another with same key
      const duplicateWorkspace = {
        ...testWorkspace,
        name: 'Different Name',
      };

      await workspacePage.goto();
      await workspacePage.clickCreate();
      await workspacePage.fillForm(duplicateWorkspace);
      const createResponse = workspacePage.page.waitForResponse(
        (res) => res.request().method() === 'POST' && res.url().includes('/api/workspaces'),
        { timeout: 10000 }
      );
      await workspacePage.clickSave();
      const resp = await createResponse;
      expect(
        [400, 409, 422],
        `expected 4xx for duplicate workspace key, got ${resp.status()}`
      ).toContain(resp.status());

      // Modal stays open and surfaces an error toast so the user can fix the key.
      await expect(page.locator(workspacePage.workspaceModal)).toBeVisible();
      await expect(
        page.locator('[data-testid="toast"][data-toast-variant="error"]').first()
      ).toBeVisible({ timeout: 5000 });
    });

    test('should require workspace name', async ({ page }) => {
      await workspacePage.goto();
      await workspacePage.clickCreate();

      // Leave name empty — submit button should be disabled
      const submitBtn = page.locator('#create-modal-submit');
      await expect(submitBtn).toBeDisabled();

      // Modal should remain open
      const modal = page.locator(workspacePage.workspaceModal);
      await expect(modal).toBeVisible();
    });
  });

  test.describe('View Workspace', () => {
    test.beforeEach(async () => {
      // Create a workspace for viewing
      await workspacePage.createWorkspace(testWorkspace);
    });

    test('should view workspace details', async () => {
      await workspacePage.goto();

      // Click on workspace
      await workspacePage.clickWorkspace(testWorkspace.name);

      // Should navigate to workspace detail page (URL uses numeric ID)
      await workspacePage.page.waitForLoadState('networkidle');

      // Verify we're on a workspace page
      await expect(workspacePage.page).toHaveURL(/\/workspaces\/\d+/);
    });

    test('should display workspace information', async () => {
      await workspacePage.goto();
      await workspacePage.clickWorkspace(testWorkspace.name);

      // Should see workspace name somewhere on the page
      await expect(workspacePage.page.locator('body')).toContainText(testWorkspace.name);
    });
  });

  test.describe('Edit Workspace', () => {
    test.beforeEach(async () => {
      // Create a workspace for editing
      await workspacePage.createWorkspace(testWorkspace);
    });

    test('should update workspace name', async () => {
      const newName = `${testWorkspace.name} - Updated`;

      await workspacePage.editWorkspace(testWorkspace.name, {
        name: newName,
      });

      // Verify update
      await workspacePage.goto();
      await workspacePage.verifyWorkspaceExists(newName);
    });

    test('should update workspace description', async ({ page }) => {
      const newDescription = 'Updated description for E2E test';

      await workspacePage.editWorkspace(testWorkspace.name, {
        description: newDescription,
      });

      // Reload the settings page and assert the persisted description
      // round-trips. The list page doesn't render description, so target
      // the settings textarea directly.
      const workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
      await page.goto(`/workspaces/${workspaceId}/settings/general`);
      await page.waitForLoadState('networkidle');
      await expect(page.locator('#workspace-description')).toHaveValue(newDescription, {
        timeout: 5000,
      });
    });

    test('should update all workspace fields', async () => {
      // Key max length is 10 chars, so use a short updated key
      const updatedData = {
        name: `${testWorkspace.name} - Fully Updated`,
        key: 'UPDWS',
        description: 'Completely updated workspace',
      };

      await workspacePage.editWorkspace(testWorkspace.name, updatedData);

      // Verify name was updated
      await workspacePage.goto();
      await workspacePage.verifyWorkspaceExists(updatedData.name);
    });
  });

  test.describe('Delete Workspace', () => {
    test.beforeEach(async () => {
      // Create a workspace for deletion
      await workspacePage.createWorkspace(testWorkspace);
    });

    test('should delete workspace', async () => {
      await workspacePage.deleteWorkspace(testWorkspace.name);

      // Verify deletion
      await workspacePage.verifyWorkspaceDoesNotExist(testWorkspace.name);
    });

    test('should confirm before deleting', async () => {
      const workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
      await workspacePage.page.goto(`/workspaces/${workspaceId}/settings/danger`);
      await workspacePage.page.waitForLoadState('networkidle');

      // Reveal the confirmation form.
      await workspacePage.page
        .locator('button.bg-red-600')
        .filter({ hasText: 'Remove Workspace' })
        .click();

      const confirmInput = workspacePage.page.locator('#delete-confirm');
      const confirmButton = workspacePage.page.locator('button:has-text("Yes, Remove Workspace")');
      await expect(confirmInput).toBeVisible({ timeout: 5000 });

      // The confirm button is wired to `disabled={deleteConfirmText !== workspace.name}`
      // — must type the exact name to enable it.
      await expect(confirmButton).toBeDisabled();
      await confirmInput.fill(`${testWorkspace.name} wrong`);
      await expect(confirmButton).toBeDisabled();
      await confirmInput.fill(testWorkspace.name);
      await expect(confirmButton).toBeEnabled();

      await Promise.all([
        workspacePage.page.waitForURL(/\/workspaces$/, { timeout: 15000 }),
        confirmButton.click(),
      ]);
      await workspacePage.verifyWorkspaceDoesNotExist(testWorkspace.name);
    });

    test('should cancel workspace deletion', async () => {
      const workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
      await workspacePage.page.goto(`/workspaces/${workspaceId}/settings/danger`);
      await workspacePage.page.waitForLoadState('networkidle');

      await workspacePage.page
        .locator('button.bg-red-600')
        .filter({ hasText: 'Remove Workspace' })
        .click();

      const confirmInput = workspacePage.page.locator('#delete-confirm');
      await expect(confirmInput).toBeVisible({ timeout: 5000 });

      await workspacePage.page.locator('[data-testid="cancel-delete-workspace"]').click();
      await expect(confirmInput).toBeHidden({ timeout: 5000 });

      // Workspace should still exist after cancel.
      await workspacePage.goto();
      await workspacePage.verifyWorkspaceExists(testWorkspace.name);
    });
  });

  test.describe('Workspace List', () => {
    test('should display multiple workspaces', async () => {
      // Create multiple workspaces
      const workspace1 = generateWorkspace('1');
      const workspace2 = generateWorkspace('2');
      const workspace3 = generateWorkspace('3');

      await workspacePage.createWorkspace(workspace1);
      await workspacePage.createWorkspace(workspace2);
      await workspacePage.createWorkspace(workspace3);

      // Go to list
      await workspacePage.goto();

      // Verify all are visible
      await workspacePage.verifyWorkspaceExists(workspace1.name);
      await workspacePage.verifyWorkspaceExists(workspace2.name);
      await workspacePage.verifyWorkspaceExists(workspace3.name);

      // Get count
      const count = await workspacePage.getWorkspaceCount();
      expect(count).toBeGreaterThanOrEqual(3);
    });

    test('should search workspaces', async ({ page }) => {
      // Search lives in the sidebar Workspaces dropdown (not the list page).
      // Seed two workspaces with distinct, unique prefixes so the filter has
      // something unambiguous to match on a shared DB.
      const stamp = Date.now();
      const alpha = generateWorkspace(`alpha-${stamp}`);
      const beta = generateWorkspace(`beta-${stamp}`);
      await workspacePage.createWorkspace(alpha);
      await workspacePage.createWorkspace(beta);

      await workspacePage.goto();
      await page.locator('[data-testid="workspaces-dropdown-trigger"]').click();
      const searchInput = page.locator('[data-testid="workspaces-search"]');
      await expect(searchInput).toBeVisible({ timeout: 5000 });

      await searchInput.fill(alpha.name);
      const visibleItems = page.locator('[data-testid="workspace-dropdown-item"]');
      await expect(visibleItems.filter({ hasText: alpha.name })).toHaveCount(1, { timeout: 5000 });
      await expect(visibleItems.filter({ hasText: beta.name })).toHaveCount(0);
    });
  });

  test.describe('Workspace Navigation', () => {
    test.beforeEach(async () => {
      await workspacePage.createWorkspace(testWorkspace);
    });

    test('should navigate to workspace via menu and support opening a new tab', async ({
      page,
    }) => {
      // Click the sidebar Workspaces dropdown, then click the workspace row
      // — the dropdown is the canonical menu-driven nav (sidebar is icon-only).
      await page.locator('[data-testid="workspaces-dropdown-trigger"]').click();
      const searchInput = page.locator('[data-testid="workspaces-search"]');
      await expect(searchInput).toBeVisible({ timeout: 5000 });
      await searchInput.fill(testWorkspace.name);

      const item = page
        .locator('[data-testid="workspace-dropdown-item"]')
        .filter({ hasText: testWorkspace.name })
        .first();
      await expect(item).toBeVisible({ timeout: 5000 });
      const workspaceId = await item.getAttribute('data-id');
      expect(workspaceId).toMatch(/^\d+$/);
      const workspaceHref = await item.getAttribute('href');
      expect(logicalPath(workspaceHref || '')).toBe(`/workspaces/${workspaceId}`);

      const currentURL = page.url();
      const workspaceTabPromise = page.context().waitForEvent('page');
      await item.click({
        modifiers: [process.platform === 'darwin' ? 'Meta' : 'Control'],
      });
      const workspaceTab = await workspaceTabPromise;
      await workspaceTab.waitForLoadState('domcontentloaded');
      await expect(workspaceTab).toHaveURL(new RegExp(`/workspaces/${workspaceId}(\\/|$|\\?)`));
      expect(page.url()).toBe(currentURL);
      await workspaceTab.close();

      await page.locator('[data-testid="workspaces-dropdown-trigger"]').click();
      await item.click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}(\\/|$|\\?)`), {
        timeout: 10000,
      });
      await expect(page.locator('body')).toContainText(testWorkspace.name);
    });

    test('should navigate to workspace backlog', async () => {
      await workspacePage.goto();

      // Click on workspace
      await workspacePage.clickWorkspace(testWorkspace.name);

      // Backlog is a first-class section of any workspace — fail loudly if the
      // link disappears rather than passing silently as the previous spec did.
      const backlogLink = workspacePage.page.locator('a:has-text("Backlog")');
      await expect(backlogLink).toBeVisible({ timeout: 5000 });
      await backlogLink.click();
      await workspacePage.page.waitForLoadState('networkidle');

      // Should be on backlog page (URL uses numeric ID, not key)
      await expect(workspacePage.page).toHaveURL(/\/workspaces\/\d+\/backlog/);
    });
  });
});
