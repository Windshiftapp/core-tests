import { createUserViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { WorkspacePage } from '../pages/workspace.page';
import { WorkspaceSettingsPage } from '../pages/workspace-settings.page';

/**
 * Workspace Settings Tests
 * Tests workspace general settings, members, and danger zone
 */

test.describe('Workspace Settings', () => {
  let settingsPage: WorkspaceSettingsPage;
  let workspacePage: WorkspacePage;
  let testWorkspace: ReturnType<typeof generateWorkspace>;
  let workspaceId: string;

  test.beforeEach(async ({ page }) => {
    settingsPage = new WorkspaceSettingsPage(page);
    workspacePage = new WorkspacePage(page);
    testWorkspace = generateWorkspace();

    // Create workspace and resolve its numeric ID (settings URLs use the ID)
    await workspacePage.createWorkspace(testWorkspace);
    workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
  });

  test.describe('General Settings', () => {
    test('should display workspace settings', async () => {
      await settingsPage.goto(workspaceId);

      // Landing on /settings redirects to /settings/general — the general form
      // mounts with the workspace name input.
      const nameInput = settingsPage.page.locator(settingsPage.nameInput);
      await expect(nameInput).toBeVisible({ timeout: 5000 });
    });

    test('should update workspace name', async () => {
      const newName = `${testWorkspace.name} - Settings Updated`;

      await settingsPage.goto(workspaceId);
      await settingsPage.updateGeneralSettings({ name: newName });

      // Verify update
      await settingsPage.goto(workspaceId);
      const nameInput = settingsPage.page.locator(settingsPage.nameInput);
      await expect(nameInput).toHaveValue(newName);
    });

    test('should update workspace description', async () => {
      const newDescription = 'Updated via settings page';

      await settingsPage.goto(workspaceId);
      await settingsPage.updateGeneralSettings({ description: newDescription });

      // Verify update
      await settingsPage.goto(workspaceId);
      const descInput = settingsPage.page.locator(settingsPage.descriptionInput);
      await expect(descInput).toHaveValue(newDescription);
    });
  });

  test.describe('Members', () => {
    test('should display members tab', async () => {
      await settingsPage.goto(workspaceId);
      await settingsPage.clickTab('Members');

      // The Members panel exposes an "Add Member" action; use it as the
      // canonical "panel rendered" signal.
      const addMemberButton = settingsPage.page.getByRole('button', { name: /add member/i });
      await expect(addMemberButton.first()).toBeVisible({ timeout: 5000 });
    });

    test('should add and remove a member', async ({ request }) => {
      // A count >= 0 check can never fail — exercise the actual add/remove
      // round-trip instead. Fresh user via API, then drive the Members panel.
      const suffix = Date.now().toString(36);
      const username = `wsmem${suffix}`;
      const user = await createUserViaAPI(request, {
        email: `${username}@example.com`,
        username,
        first_name: 'Member',
        last_name: 'RoundTrip',
        password_hash: 'Test1234!pw',
      });

      const rolesResp = await request.get('/api/workspace-roles', {
        headers: { 'Sec-Fetch-Site': 'same-origin' },
      });
      expect(rolesResp.ok()).toBeTruthy();
      const rolesBody = await rolesResp.json();
      const roles: Array<{ id: number; name: string }> = rolesBody.data ?? rolesBody;
      const editorRole = roles.find((r) => r.name === 'Editor');
      if (!editorRole) throw new Error('seeded Editor workspace role missing');

      await settingsPage.goto(workspaceId);
      const before = await settingsPage.getMemberCount();

      await settingsPage.addMember(user.id, username, editorRole.id);
      await settingsPage.verifyMemberExists(username);
      expect(await settingsPage.getMemberCount()).toBe(before + 1);

      await settingsPage.removeMember(username);
      expect(await settingsPage.getMemberCount()).toBe(before);
    });
  });

  test.describe('Danger Zone', () => {
    test('should display danger zone', async () => {
      await settingsPage.goto(workspaceId);
      await settingsPage.clickTab('Remove Workspace');

      // Should see delete workspace button
      const deleteButton = settingsPage.page.locator(settingsPage.deleteWorkspaceButton);
      await expect(deleteButton).toBeVisible({ timeout: 5000 });
    });

    test('should delete workspace from settings', async () => {
      await settingsPage.goto(workspaceId);
      await settingsPage.deleteWorkspace(testWorkspace.name);

      // Verify workspace was deleted
      await workspacePage.goto();
      await workspacePage.verifyWorkspaceDoesNotExist(testWorkspace.name, workspaceId);
    });
  });
});
