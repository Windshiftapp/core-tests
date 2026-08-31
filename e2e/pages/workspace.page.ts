import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Workspace Management
 */
export class WorkspacePage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  // Selectors
  readonly workspacesLink = 'a:has-text("Workspaces")';
  readonly createButton = 'button:has-text("Add Workspace")';
  readonly workspaceModal = 'div[role="dialog"]';
  readonly nameInput = '#workspace-name';
  readonly keyInput = '#workspace-key';
  readonly workspaceRow = 'tbody tr';
  readonly successToast =
    'text=created successfully, text=updated successfully, text=deleted successfully';
  readonly errorToast = '.error, .error-message, [role="alert"]';

  /**
   * Navigate to workspaces page
   */
  async goto() {
    await this.page.goto('/workspaces');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Navigate via menu
   */
  async navigateViaMenu() {
    await this.page.click(this.workspacesLink);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click create workspace button and wait for the form to mount.
   * The CreateModal is lazily loaded — the dialog backdrop appears before the form.
   */
  async clickCreate() {
    await this.page.click(this.createButton);
    // Wait for the workspace name input in the modal (uses placeholder-based locator
    // because the create modal form inputs may not have id attributes in DOM)
    await this.page
      .getByPlaceholder('Workspace name')
      .waitFor({ state: 'visible', timeout: 10000 });
  }

  /**
   * Fill workspace form in the create modal.
   * Uses pressSequentially() instead of fill() to trigger Svelte 5 $state reactivity
   * via real input events per keystroke.
   * The name field auto-generates the key via onNameInput(), so key is only filled
   * if explicitly provided and different from what would be auto-generated.
   * Description uses MilkdownEditor (rich text editor) — insertText() avoids global hotkeys.
   */
  async fillForm(data: { name: string; key?: string; description?: string }) {
    // Name field — clear and type char-by-char to trigger Svelte reactivity
    const nameField = this.page.getByPlaceholder('Workspace name');
    await nameField.click();
    await nameField.fill(''); // clear any existing value
    await nameField.pressSequentially(data.name, { delay: 30 });

    // Key field — only fill if explicitly provided (name auto-generates key)
    if (data.key) {
      const keyField = this.page.getByPlaceholder('Workspace key');
      await keyField.click();
      await keyField.fill(''); // clear auto-generated value
      await keyField.pressSequentially(data.key, { delay: 30 });
    }

    // Description is a MilkdownEditor (ProseMirror). Use insertText to avoid
    // character-by-character typing which can lose focus to global hotkeys.
    if (data.description) {
      const dialog = this.page.locator(this.workspaceModal);
      const editor = dialog.locator('.ProseMirror');
      await editor.waitFor({ state: 'attached', timeout: 5000 });
      await editor.click();
      await this.page.keyboard.insertText(data.description);
    }
  }

  /**
   * Submit the create modal form by clicking the submit button.
   */
  async clickSave() {
    const submitBtn = this.page.locator('#create-modal-submit');
    await submitBtn.waitFor({ state: 'visible', timeout: 5000 });
    await submitBtn.click();
    // `createWorkspace` follows up with a strict "modal detached" wait, so
    // nothing more is needed here.
  }

  /**
   * Create a new workspace
   */
  async createWorkspace(data: { name: string; key?: string; description?: string }) {
    await this.goto();
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();

    // After creation, the app closes the modal and navigates to /workspaces/{id}.
    // Wait for modal to disappear (detached or hidden) — works reliably for SPA navigation.
    await this.page.locator(this.workspaceModal).waitFor({ state: 'detached', timeout: 15000 });

    // Navigate to list and verify workspace exists
    await this.goto();
    await this.verifyWorkspaceExists(data.name);
  }

  /**
   * Find workspace by name
   */
  async findWorkspaceByName(name: string) {
    // Find table row containing the workspace name
    return this.page.locator(`${this.workspaceRow}:has-text("${name}")`).first();
  }

  /**
   * Verify workspace exists
   */
  async verifyWorkspaceExists(name: string) {
    const workspace = await this.findWorkspaceByName(name);
    await expect(workspace).toBeVisible({ timeout: 10000 });
  }

  /**
   * Click on a workspace to view details
   */
  async clickWorkspace(name: string) {
    const workspace = await this.findWorkspaceByName(name);
    await workspace.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Get the numeric workspace ID by navigating to the workspace list,
   * clicking the workspace row, and extracting the ID from the resulting URL.
   */
  async getWorkspaceId(name: string): Promise<string> {
    await this.goto();
    await this.clickWorkspace(name);
    const match = this.page.url().match(/\/workspaces\/(\d+)/);
    if (!match) throw new Error(`Cannot extract workspace ID from URL: ${this.page.url()}`);
    return match[1];
  }

  /**
   * Open dropdown menu for a workspace row
   */
  private async openRowDropdown(name: string) {
    const row = await this.findWorkspaceByName(name);
    await row.locator('button').last().click();
    await this.page
      .locator('button[role="menuitem"]')
      .first()
      .waitFor({ state: 'visible', timeout: 5000 });
  }

  /**
   * Click a menuitem from the open dropdown
   */
  private async clickMenuItem(text: string) {
    await this.page.locator('button[role="menuitem"]').filter({ hasText: text }).click();
  }

  /**
   * Extract workspace ID from the current URL after navigating to a workspace.
   * URL pattern: /workspaces/{id} or /workspaces/{id}/settings/...
   */
  private getWorkspaceIdFromUrl(): string {
    const match = this.page.url().match(/\/workspaces\/(\d+)/);
    if (!match) throw new Error(`Cannot extract workspace ID from URL: ${this.page.url()}`);
    return match[1];
  }

  /**
   * Edit a workspace.
   * The "Edit" dropdown item navigates to /workspaces/{id}. From there we go
   * to the settings/general tab and update fields.
   */
  async editWorkspace(
    currentName: string,
    newData: {
      name?: string;
      key?: string;
      description?: string;
    }
  ) {
    await this.goto();

    // Open dropdown and click Edit — this navigates to /workspaces/{id}
    await this.openRowDropdown(currentName);
    await this.clickMenuItem('Edit');
    await this.page.waitForLoadState('networkidle');

    // Extract workspace ID from URL then navigate to settings/general
    const workspaceId = this.getWorkspaceIdFromUrl();
    await this.page.goto(`/workspaces/${workspaceId}/settings/general`);
    await this.page.waitForLoadState('networkidle');

    // Settings page uses standard Input/Textarea components with proper IDs
    if (newData.name) {
      await this.page.fill(this.nameInput, newData.name);
    }
    if (newData.key) {
      await this.page.fill(this.keyInput, newData.key);
    }
    if (newData.description) {
      // Settings page uses a plain Textarea, not MilkdownEditor
      await this.page.fill('#workspace-description', newData.description);
    }

    // Click "Save Changes" and wait for the PATCH to complete
    const saveRequest = this.page
      .waitForResponse(
        (res) => res.request().method() === 'PATCH' && res.url().includes('/workspaces/'),
        { timeout: 10000 }
      )
      .catch(() => null);
    await this.page.click('button:has-text("Save Changes")');
    await saveRequest;

    // If name was updated, navigate to list and verify
    if (newData.name) {
      await this.goto();
      await this.verifyWorkspaceExists(newData.name);
    }
  }

  /**
   * Delete a workspace.
   * Navigate to workspace settings → Remove Workspace tab → confirm deletion.
   */
  async deleteWorkspace(name: string) {
    await this.goto();

    // Open dropdown and click Edit — navigates to /workspaces/{id}
    await this.openRowDropdown(name);
    await this.clickMenuItem('Edit');
    await this.page.waitForLoadState('networkidle');

    // Navigate to danger tab (labeled "Remove Workspace")
    const workspaceId = this.getWorkspaceIdFromUrl();
    await this.page.goto(`/workspaces/${workspaceId}/settings/danger`);
    await this.page.waitForLoadState('networkidle');

    // Click the red "Remove Workspace" button (not the tab) to reveal confirmation form.
    // Use the bg-red-600 class to disambiguate from the tab button with the same text.
    await this.page.locator('button.bg-red-600').filter({ hasText: 'Remove Workspace' }).click();

    // Wait for confirmation input to appear
    await this.page.locator('#delete-confirm').waitFor({ state: 'visible', timeout: 5000 });

    // Type the workspace name in the confirmation input
    await this.page.fill('#delete-confirm', name);

    // Click "Yes, Remove Workspace"
    await this.page.click('button:has-text("Yes, Remove Workspace")');

    // After deletion, the app redirects to /workspaces after a 1s delay
    await this.page.waitForURL(/\/workspaces$/, { timeout: 15000 });
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify workspace does not exist
   */
  async verifyWorkspaceDoesNotExist(name: string, workspaceId?: string) {
    const workspace = workspaceId
      ? this.page.getByTestId(`workspace-row-${workspaceId}`)
      : this.page.locator(`${this.workspaceRow}:has-text("${name}")`);
    await expect(workspace).not.toBeVisible({ timeout: 5000 });
  }

  /**
   * Get workspace count
   */
  async getWorkspaceCount(): Promise<number> {
    const workspaces = await this.page.locator(this.workspaceRow).count();
    return workspaces;
  }

  /**
   * Search for workspace
   */
  async searchWorkspace(query: string) {
    const searchInput = this.page.locator('input[type="search"], input[placeholder*="Search"]');
    await searchInput.fill(query);
    // Debounced filter refreshes the list; wait for the network to settle.
    await this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }
}
