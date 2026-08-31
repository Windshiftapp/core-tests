import { expect, type Page } from '../fixtures/context-path';
import { pickUser } from './teams.page';

/**
 * Page Object for Workspace Settings
 *
 * Settings URLs use the numeric workspace ID (not the string key). Use
 * `WorkspacePage.getWorkspaceId(name)` to resolve a key/name to the ID first.
 */
export class WorkspaceSettingsPage {
  constructor(public readonly page: Page) {}

  readonly settingsLink = 'a:has-text("Settings"), [data-testid="workspace-settings"]';

  // Admin navigation is a folded sidebar (data-testid="workspace-admin-nav"),
  // one link per module (data-testid="workspace-admin-nav-<id>"). The legacy
  // label-based `clickTab` maps friendly labels to those module ids.
  readonly adminNav = '[data-testid="workspace-admin-nav"]';
  readonly backLink = '[data-testid="workspace-back-link"]';

  private static readonly MODULE_ID_BY_LABEL: Record<string, string> = {
    General: 'general',
    Categories: 'categories',
    Members: 'members',
    'Configuration Sets': 'configuration',
    'Source Control': 'source-control',
    'Issue Sync': 'issue-sync',
    Recurrence: 'recurrence',
    'Remove Workspace': 'danger',
  };

  readonly nameInput = 'input[name="name"], #workspace-name';
  readonly keyInput = 'input[name="key"], #workspace-key';
  readonly descriptionInput = 'textarea[name="description"], #workspace-description';
  readonly saveButton = 'button:has-text("Save Changes")';

  readonly addMemberButton = 'button:has-text("Add Member"), button:has-text("Add")';
  // The Members module has a role-summary table above the assignment table.
  // Assignment rows are the only rows with a role-action dropdown.
  readonly memberRow =
    '[data-testid="workspace-member-assignments"] tbody tr[data-testid^="workspace-member-"]';
  readonly removeMemberButton = 'button:has-text("Remove"), [aria-label="Remove"]';
  readonly memberSelect = 'select[name="user"], [name="user_id"]';

  // Action button in the Danger Zone panel (red "Remove Workspace" button,
  // not the tab with the same label).
  readonly deleteWorkspaceButton = '[data-testid="delete-workspace-open"]';
  readonly confirmDeleteInput = '[data-testid="delete-workspace-confirm-name"]';
  readonly confirmDeleteButton = '[data-testid="delete-workspace-confirm"]';

  /**
   * Navigate to workspace settings. `workspaceId` must be the numeric ID.
   */
  async goto(workspaceId: string) {
    await this.page.goto(`/workspaces/${workspaceId}/settings`);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Navigate to a settings module via the folded admin sidebar. Accepts the
   * friendly module label (legacy tab name) and clicks the matching sidebar
   * link by its data-testid.
   */
  async clickTab(tabName: string) {
    const moduleId = WorkspaceSettingsPage.MODULE_ID_BY_LABEL[tabName];
    if (moduleId) {
      await this.page.locator(`[data-testid="workspace-admin-nav-${moduleId}"]`).click();
    } else {
      await this.page.locator(this.adminNav).getByRole('link', { name: tabName }).first().click();
    }
    // The target module may fetch — wait for the network to settle before
    // downstream selectors probe for panel elements.
    await this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }

  /**
   * Update general settings
   */
  async updateGeneralSettings(data: { name?: string; description?: string }) {
    await this.clickTab('General');

    // Wait for the workspace to finish loading (name input populated) before
    // editing — an in-flight load can otherwise clobber freshly typed values.
    await expect(this.page.locator(this.nameInput)).not.toHaveValue('', { timeout: 5000 });

    if (data.name) {
      await this.page.fill(this.nameInput, data.name);
    }
    if (data.description) {
      await this.page.fill(this.descriptionInput, data.description);
    }

    const saveRequest = this.page
      .waitForResponse(
        (res) => res.request().method() === 'PATCH' && res.url().includes('/workspaces/'),
        { timeout: 10000 }
      )
      .catch(() => null);
    await this.page.click(this.saveButton);
    await saveRequest;
  }

  /**
   * Add member to workspace via the Add Workspace Member modal.
   *
   * The modal uses a portalled UserPicker (searched by username via the
   * shared pickUser helper) and the custom Select combobox for the role —
   * both render their menus outside the dialog element.
   */
  async addMember(userId: number, username: string, roleId: number) {
    await this.clickTab('Members');
    await this.page
      .getByRole('button', { name: /add member/i })
      .first()
      .click();
    const dialog = this.page.locator('div[role="dialog"]');
    await dialog.waitFor({ state: 'visible', timeout: 5000 });

    await pickUser(
      this.page,
      dialog.locator('[data-testid="user-picker-trigger"]'),
      userId,
      username
    );

    // Custom Select combobox: options carry data-option-id (the role id) in a
    // portalled listbox. Wait for the listbox to close after selection so it
    // can't intercept the submit click (a stuck-open listbox here caught a
    // real onchange bug in WorkspaceMembers.svelte).
    await dialog.locator('button[role="combobox"]').click();
    const listbox = this.page.locator('[role="listbox"][data-melt-popover-content]');
    await listbox.locator(`[role="option"][data-option-id="${roleId}"]`).click();
    await listbox.waitFor({ state: 'detached', timeout: 5000 });

    await dialog.getByRole('button', { name: 'Add Member' }).click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
    // Expect the new member row to appear in the list. NB: `memberRow` is a
    // comma-separated selector union — an inline `:has-text()` suffix would
    // only apply to its last alternative, so filter instead.
    await this.memberRowFor(username).waitFor({ state: 'visible', timeout: 10000 });
  }

  /** Row in the members table for the given username/email/display name. */
  private memberRowFor(username: string) {
    return this.page.locator(this.memberRow).filter({ hasText: username }).first();
  }

  /**
   * Remove a member's role assignment via the row action menu. Removing the
   * user's only role drops them from the list entirely.
   */
  async removeMember(username: string, roleName = 'Editor') {
    await this.clickTab('Members');
    const memberRow = this.memberRowFor(username);
    await memberRow.locator('.dropdown-trigger button').click();
    await this.page
      .locator(`button[data-menu-item]:has-text("Remove ${roleName}")`)
      .first()
      .click();
    await this.page.getByTestId('dialog-confirm').click();
    // Wait for the row to be removed from the DOM
    await memberRow.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Verify member is in workspace
   */
  async verifyMemberExists(username: string) {
    await this.clickTab('Members');
    await expect(this.memberRowFor(username)).toBeVisible({ timeout: 10000 });
  }

  /**
   * Get member count
   */
  async getMemberCount(): Promise<number> {
    await this.clickTab('Members');
    await this.page.getByTestId('workspace-member-assignments').waitFor({
      state: 'visible',
      timeout: 10000,
    });
    await this.page.locator(this.memberRow).first().waitFor({ state: 'visible', timeout: 10000 });
    return this.page.locator(this.memberRow).count();
  }

  /**
   * Delete workspace from danger zone
   */
  async deleteWorkspace(workspaceName: string) {
    const workspaceId = this.page.url().match(/\/workspaces\/(\d+)/)?.[1];
    if (!workspaceId) throw new Error(`Cannot extract workspace ID from URL: ${this.page.url()}`);
    await this.clickTab('Remove Workspace');
    await this.page.click(this.deleteWorkspaceButton);

    // Wait for the confirmation input to appear, then type the workspace name
    const confirmInput = this.page.locator(this.confirmDeleteInput);
    await confirmInput.waitFor({ state: 'visible', timeout: 5000 });
    await confirmInput.fill(workspaceName);

    // After delete the backend redirects away from the settings page; wait for
    // the URL to leave /settings so downstream assertions see the new location.
    const deleteResponse = this.page.waitForResponse(
      (response) =>
        response.request().method() === 'DELETE' &&
        new URL(response.url()).pathname.endsWith(`/api/workspaces/${workspaceId}`)
    );
    const [, response] = await Promise.all([
      this.page.waitForURL((url) => !url.pathname.endsWith('/settings'), { timeout: 10000 }),
      deleteResponse,
      this.page.click(this.confirmDeleteButton),
    ]);
    expect(response.ok(), `delete workspace response: ${response.status()}`).toBeTruthy();
  }
}
