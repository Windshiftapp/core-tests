import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for the Groups admin UI at /admin/groups.
 *
 * Historical note: this used to be called TeamPage because the upstream
 * spec was "Team Management". The code always drove /admin/groups — there
 * is a separate (FE-unwired) Teams backend at /api/teams that this object
 * does NOT cover.
 */
export class GroupPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly groupModal = 'div[role="dialog"]';
  readonly nameInput = '#name';
  readonly descriptionInput = '#description';
  readonly groupRow = 'tbody tr';
  readonly createButton =
    'button:has-text("Create Group"), button:has-text("Add Group"), button:has-text("New Group")';

  /**
   * Navigate to groups page
   */
  async goto() {
    await this.page.goto('/admin/groups');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click create group button
   */
  async clickCreate() {
    await this.page.click(this.createButton);
    await this.page.waitForSelector(this.groupModal, { timeout: 5000 });
  }

  /**
   * Fill group form
   */
  async fillForm(data: { name: string; description?: string }) {
    await this.page.fill(this.nameInput, data.name);
    if (data.description) {
      await this.page.fill(this.descriptionInput, data.description);
    }
  }

  /**
   * Click save button (scoped to dialog)
   */
  async clickSave() {
    const dialog = this.page.locator(this.groupModal).last();
    await dialog.getByTestId('dialog-confirm').click();
    await expect(dialog).toBeHidden({ timeout: 10000 });
  }

  /**
   * Create a new group
   */
  async createGroup(data: { name: string; description?: string }) {
    await this.goto();
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();
  }

  /**
   * Find group by name
   */
  findGroupByName(name: string) {
    return this.page.locator(`${this.groupRow}:has-text("${name}")`).first();
  }

  /**
   * Verify group exists
   */
  async verifyGroupExists(name: string) {
    await this.goto();
    const group = this.findGroupByName(name);
    await expect(group).toBeVisible({ timeout: 10000 });
  }

  /**
   * Verify group does not exist
   */
  async verifyGroupDoesNotExist(name: string) {
    const group = this.page.locator(`${this.groupRow}:has-text("${name}")`);
    await expect(group).not.toBeVisible({ timeout: 5000 });
  }

  /**
   * Click on a group to view details
   */
  async clickGroup(name: string) {
    const group = this.findGroupByName(name);
    await group.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Open dropdown menu for a group row
   */
  private async openRowDropdown(name: string) {
    const row = this.findGroupByName(name);
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
   * Edit a group
   */
  async editGroup(currentName: string, newData: { name?: string; description?: string }) {
    await this.goto();

    // Open dropdown and click Edit
    await this.openRowDropdown(currentName);
    await this.clickMenuItem('Edit');
    await this.page.waitForSelector(this.groupModal, { timeout: 5000 });

    if (newData.name) {
      await this.page.fill(this.nameInput, newData.name);
    }
    if (newData.description) {
      await this.page.fill(this.descriptionInput, newData.description);
    }

    await this.clickSave();
  }

  /**
   * Delete a group
   */
  async deleteGroup(name: string) {
    await this.goto();

    // Open dropdown and click Delete
    await this.openRowDropdown(name);
    await this.clickMenuItem('Delete');

    // Wait for the confirmation dialog to appear
    const confirmDialog = this.page.locator(this.groupModal);
    await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });

    // Confirm deletion and wait for dialog to close
    const confirmButton = confirmDialog.locator('button:has-text("Delete")').last();
    await confirmButton.click();
    await confirmDialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Open members management for a group
   */
  async openMembers(groupName: string) {
    await this.goto();
    await this.openRowDropdown(groupName);
    await this.clickMenuItem('Members');
    await this.page.waitForSelector(this.groupModal, { timeout: 5000 });
  }

  /**
   * Add member to group via the members modal
   */
  async addMember(groupName: string, memberName: string) {
    await this.openMembers(groupName);
    const dialog = this.page.locator(this.groupModal);

    // Open the UserPicker popover (combobox trigger lives in the dialog)
    await dialog.getByTestId('user-picker-trigger').click();

    // The popover is portalled to <body>, so the search input is NOT inside the dialog
    const searchInput = this.page.getByTestId('user-picker-search');
    await searchInput.waitFor({ state: 'visible', timeout: 5000 });

    // UserPicker fetches /api/users on mount; on a cold run the response can
    // take >5s, and the picker's filter is purely client-side so an empty
    // usersList means zero <option>s render. Wait for the initial load to
    // produce at least one option before searching, otherwise the
    // option-visible check below races the response.
    await this.page.getByRole('option').first().waitFor({ state: 'visible', timeout: 15000 });

    await searchInput.fill(memberName);

    // Filter narrows the listbox to the typed user; pick it
    const option = this.page.getByRole('option', { name: new RegExp(memberName, 'i') }).first();
    await option.waitFor({ state: 'visible', timeout: 5000 });
    await option.click();

    // Commit by clicking Add in the dialog (appears once a user is staged)
    const addButton = dialog.getByRole('button', { name: /^Add\b/i }).last();
    await addButton.waitFor({ state: 'visible', timeout: 5000 });
    await addButton.click();

    // Wait for the new member to appear in the members list inside the dialog
    await dialog
      .locator(`text=${memberName}`)
      .first()
      .waitFor({ state: 'visible', timeout: 10000 });
  }

  /**
   * Remove member from group
   */
  async removeMember(groupName: string, memberName: string) {
    await this.openMembers(groupName);

    // The email text in GroupManager.svelte sits in a deeply nested
    // <div class="text-xs">; the Remove <button> is on the row container
    // four levels up. Anchor on the email and walk to the nearest ancestor
    // that actually contains a button.
    const dialog = this.page.locator(this.groupModal);
    const memberRow = dialog
      .getByText(memberName, { exact: false })
      .locator('xpath=ancestor::*[.//button][1]');
    const removeButton = memberRow.getByRole('button', { name: /remove/i });
    await removeButton.click();

    // GroupManager.removeMember opens a ConfirmDialog before hitting the
    // API. Both that ConfirmDialog AND the underlying Members modal expose
    // a `data-testid="dialog-confirm"` button (DialogFooter defaults the
    // testid). Scope to the ConfirmDialog by `aria-labelledby="dialog-title"`
    // (set by ModalBackdrop only — Modal.svelte does not).
    const confirmDialog = this.page.locator('[role="dialog"][aria-labelledby="dialog-title"]');
    await confirmDialog.locator('[data-testid="dialog-confirm"]').click();

    // Wait for the member's email to disappear from the members dialog
    await dialog
      .getByText(memberName, { exact: false })
      .waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Verify member is in group
   */
  async verifyMemberInGroup(groupName: string, memberName: string) {
    await this.openMembers(groupName);
    const memberText = this.page.locator(this.groupModal).locator(`text=${memberName}`);
    await expect(memberText).toBeVisible({ timeout: 10000 });
  }

  /**
   * Get group count
   */
  async getGroupCount(): Promise<number> {
    await this.goto();
    return this.page.locator(this.groupRow).count();
  }
}
