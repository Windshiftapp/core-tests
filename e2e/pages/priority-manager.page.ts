import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Priority Manager Admin UI
 */
export class PriorityManagerPage {
  constructor(private page: Page) {}

  // Selectors
  readonly createButton = 'button:has-text("Create Priority")';
  readonly modal = 'div[role="dialog"]';
  readonly nameInput = '#name';
  readonly descriptionInput = '#description';
  readonly sortOrderInput = '#sort_order';
  readonly defaultToggle = 'button[role="switch"]';
  readonly tableRow = 'tbody tr';

  /**
   * Navigate to priority manager admin page
   */
  async goto() {
    await this.page.goto('/admin/priorities');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click the "Create Priority" button
   */
  async clickCreatePriority() {
    await this.page.click(this.createButton);
    await this.page.waitForSelector(this.modal, { timeout: 5000 });
  }

  /**
   * Fill the priority name input
   */
  async fillName(name: string) {
    await this.page.fill(this.nameInput, name);
  }

  /**
   * Fill the priority description textarea
   */
  async fillDescription(description: string) {
    const modal = this.page.locator(this.modal);
    await modal.locator(this.descriptionInput).fill(description);
  }

  /**
   * Fill the sort order input
   */
  async fillSortOrder(sortOrder: number) {
    const modal = this.page.locator(this.modal);
    const input = modal.locator(this.sortOrderInput);
    await input.fill(String(sortOrder));
  }

  /**
   * Click the save/create button in the dialog footer
   */
  async clickSave() {
    const dialog = this.page.locator(this.modal);
    const confirmButton = dialog
      .locator('button:has-text("Create"), button:has-text("Update")')
      .last();
    await confirmButton.click();
    // The `createPriority`/`editPriority` flows follow up with an explicit
    // "modal hidden" wait, so we don't duplicate it here.
  }

  /**
   * Create a priority through the UI
   */
  async createPriority(data: { name: string; description?: string; sortOrder?: number }) {
    await this.clickCreatePriority();
    await this.fillName(data.name);

    if (data.description) {
      await this.fillDescription(data.description);
    }

    if (data.sortOrder !== undefined) {
      await this.fillSortOrder(data.sortOrder);
    }

    await this.clickSave();

    // Wait for modal to close
    await this.page.waitForSelector(this.modal, { state: 'hidden', timeout: 10000 });
  }

  /**
   * Find a priority row by name
   */
  findPriorityByName(name: string) {
    return this.page.locator(`${this.tableRow}:has-text("${name}")`).first();
  }

  /**
   * Verify a priority exists in the table
   */
  async verifyPriorityExists(name: string) {
    const row = this.findPriorityByName(name);
    await expect(row).toBeVisible({ timeout: 10000 });
  }

  /**
   * Verify a priority does not exist in the table
   */
  async verifyPriorityDoesNotExist(name: string) {
    const row = this.page.locator(`${this.tableRow}:has-text("${name}")`);
    await expect(row).not.toBeVisible({ timeout: 5000 });
  }

  /**
   * Edit a priority via the row's action dropdown
   */
  async editPriority(
    currentName: string,
    newData: {
      name?: string;
      description?: string;
      sortOrder?: number;
    }
  ) {
    const row = this.findPriorityByName(currentName);

    // Click the action dropdown trigger on the row, then pick "Edit"
    const actionButton = row.locator('button').last();
    await actionButton.click();
    const editItem = this.page.locator('button[role="menuitem"]').filter({ hasText: 'Edit' });
    await editItem.waitFor({ state: 'visible', timeout: 5000 });
    await editItem.click();
    await this.page.waitForSelector(this.modal, { timeout: 5000 });

    if (newData.name !== undefined) {
      await this.page.fill(this.nameInput, newData.name);
    }

    if (newData.description !== undefined) {
      await this.fillDescription(newData.description);
    }

    if (newData.sortOrder !== undefined) {
      await this.fillSortOrder(newData.sortOrder);
    }

    await this.clickSave();

    // Wait for modal to close
    await this.page.waitForSelector(this.modal, { state: 'hidden', timeout: 10000 });
  }

  /**
   * Delete a priority via the row's action dropdown
   */
  async deletePriority(name: string) {
    const row = this.findPriorityByName(name);

    // Click the action dropdown trigger on the row
    const actionButton = row.locator('button').last();
    await actionButton.click();
    const deleteMenuItem = this.page
      .locator('button[role="menuitem"]')
      .filter({ hasText: 'Delete' });
    await deleteMenuItem.waitFor({ state: 'visible', timeout: 5000 });

    // Click "Delete" from the dropdown menu and wait for the confirmation dialog
    await deleteMenuItem.click();
    const confirmDialog = this.page.locator(this.modal);
    await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });

    // Confirm deletion (button inside the confirm dialog, not a menuitem)
    const confirmButton = confirmDialog.locator('button:has-text("Delete")').last();
    await confirmButton.click();
    await row.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Get the error message displayed on the page
   */
  async getErrorMessage() {
    const errorEl = this.page.locator('.error');
    if (await errorEl.isVisible()) {
      return errorEl.textContent();
    }
    return null;
  }
}
