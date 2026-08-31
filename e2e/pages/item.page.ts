import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Work Item Management
 */
export class ItemPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  // Selectors
  readonly createItemButton = '#global-create-button';
  readonly itemModal = 'div[role="dialog"], .modal, .item-modal';
  readonly titleInput = '#work-item-title';
  readonly descriptionInput =
    'textarea[name="description"], textarea[placeholder*="description"], .ProseMirror';
  readonly statusSelect = 'select[name="status"], [name="status"]';
  readonly prioritySelect = 'select[name="priority"], [name="priority"]';
  readonly assigneeSelect = 'select[name="assignee"], [name="assignee"]';
  readonly parentSelect = 'select[name="parent"], [name="parent_id"]';
  readonly saveButton = '#create-modal-submit';
  readonly cancelButton = 'button:has-text("Cancel"), button:has-text("Close")';
  readonly itemRow = '.item-row, tr, [data-testid="item-row"]';
  readonly itemCard = '.item-card, [data-testid="item-card"]';
  readonly itemKey = '.item-key, [data-testid="item-key"]';
  readonly editButton = 'button:has-text("Edit"), [aria-label="Edit"]';
  readonly deleteButton = 'button:has-text("Delete"), [aria-label="Delete"]';
  readonly confirmDeleteButton = 'button:has-text("Confirm"), button:has-text("Delete")';
  readonly backlogLink = 'a:has-text("Backlog"), nav a[href*="/backlog"]';

  /**
   * Navigate to workspace backlog
   */
  async gotoWorkspaceBacklog(workspaceKey: string) {
    await this.page.goto(`/workspaces/${workspaceKey}/backlog`);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click create item button
   */
  async clickCreate() {
    await this.page.click(this.createItemButton);
    await this.page.waitForSelector(this.itemModal, { timeout: 5000 });
  }

  /**
   * Fill item form
   */
  async fillForm(data: {
    title: string;
    description?: string;
    status?: string;
    priority?: string;
    parent?: string;
  }) {
    const titleField = this.page.locator(this.titleInput);
    await titleField.waitFor({ state: 'visible', timeout: 5000 });
    await titleField.fill(data.title);

    if (data.description) {
      // CreateModal uses MilkdownEditor (ProseMirror)
      const editor = this.page.locator('div[role="dialog"] .ProseMirror').first();
      await editor.click();
      await this.page.keyboard.insertText(data.description);
    }

    // Note: status, priority, and parent are not available in the create modal.
    // They must be set after item creation via inline editing on the detail page.
  }

  /**
   * Click save button in the create modal
   */
  async clickSave() {
    const submitBtn = this.page.locator(this.saveButton);
    await submitBtn.waitFor({ state: 'visible', timeout: 5000 });
    await submitBtn.click();
    // Wait for modal to close after creation
    await this.page.locator('div[role="dialog"]').waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Create a new item via the global create modal.
   * Navigates to the workspace backlog first so the modal auto-selects the workspace.
   */
  async createItem(
    workspaceId: string,
    data: {
      title: string;
      description?: string;
      status?: string;
      priority?: string;
      parent?: string;
    }
  ) {
    await this.gotoWorkspaceBacklog(workspaceId);
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();
  }

  /**
   * Find item by title
   */
  async findItemByTitle(title: string) {
    return this.page
      .locator(`${this.itemRow}:has-text("${title}"), ${this.itemCard}:has-text("${title}")`)
      .first();
  }

  /**
   * Find item by key (e.g., "E2E-123")
   */
  async findItemByKey(key: string) {
    return this.page.locator(`${this.itemKey}:has-text("${key}")`).first();
  }

  /**
   * Verify item exists
   */
  async verifyItemExists(title: string) {
    const item = await this.findItemByTitle(title);
    await expect(item).toBeVisible({ timeout: 10000 });
  }

  /**
   * Click on an item to view details
   */
  async clickItem(title: string) {
    const item = await this.findItemByTitle(title);
    await item.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Open item detail modal by clicking an item on the backlog
   */
  async openItemDetailModal(title: string) {
    const item = await this.findItemByTitle(title);
    await item.click();
    await this.page.getByText('Work Item Details').waitFor({ state: 'visible', timeout: 10000 });
  }

  /**
   * Close the item detail modal
   */
  async closeItemDetailModal() {
    await this.page.keyboard.press('Escape');
    await this.page.getByText('Work Item Details').waitFor({ state: 'detached', timeout: 5000 });
  }

  /**
   * Edit title inline in item detail modal: click title → type → Enter
   */
  async editTitleInline(newTitle: string) {
    const dialog = this.page.locator('[role="dialog"]');
    const titleButton = dialog.locator('button.title-button');
    await titleButton.click();
    const titleInput = dialog.locator('input[type="text"]').first();
    await titleInput.fill(newTitle);
    await titleInput.press('Enter');
    // Edit commits when the input is replaced by the button showing the new title
    await titleInput.waitFor({ state: 'detached', timeout: 10000 });
    await expect(dialog.locator('button.title-button')).toContainText(newTitle, { timeout: 5000 });
  }

  /**
   * Change status via ItemPicker in the item detail sidebar
   */
  async changeStatus(statusName: string) {
    const dialog = this.page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="status-field"]').click();
    const option = this.page.locator('[role="option"]').filter({ hasText: statusName });
    await option.click();
    // The popover closes on selection and the field reflects the new value
    await option.waitFor({ state: 'detached', timeout: 5000 });
    await expect(dialog.locator('[data-testid="status-field"]')).toContainText(statusName, {
      timeout: 5000,
    });
  }

  /**
   * Change priority via ItemPicker in the item detail sidebar
   */
  async changePriority(priorityName: string) {
    const dialog = this.page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="priority-field"]').click();
    const option = this.page.locator('[role="option"]').filter({ hasText: priorityName });
    await option.click();
    await option.waitFor({ state: 'detached', timeout: 5000 });
    await expect(dialog.locator('[data-testid="priority-field"]')).toContainText(priorityName, {
      timeout: 5000,
    });
  }

  /**
   * Delete item via kebab menu in item detail modal (simple case, no children)
   */
  async deleteItemViaModal() {
    const dialog = this.page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="item-detail-actions"] button').first().click();
    await this.page
      .locator('button[role="menuitem"]')
      .filter({ hasText: /delete/i })
      .click();
    await this.page
      .locator('[data-testid="delete-item-dialog"]')
      .waitFor({ state: 'visible', timeout: 5000 });
    await this.page
      .locator('[data-testid="delete-item-dialog"] button')
      .filter({ hasText: /delete/i })
      .last()
      .click();
    await this.page
      .locator('[data-testid="delete-item-dialog"]')
      .waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Create a child item via the "Child" button in item detail modal.
   * Note: the item detail modal stays open behind the create modal,
   * so we can't use clickSave() which waits for all dialogs to detach.
   */
  async createChildItemViaModal(childTitle: string) {
    const dialog = this.page.locator('[role="dialog"]').first();
    await dialog.locator('button').filter({ hasText: /child/i }).click();
    // Wait for create modal to show "New Child Item" header — confirms parent is set
    await this.page.getByText('New Child Item').waitFor({ state: 'visible', timeout: 10000 });
    const titleField = this.page.locator(this.titleInput);
    await titleField.waitFor({ state: 'visible', timeout: 5000 });
    await titleField.fill(childTitle);
    const submitBtn = this.page.locator(this.saveButton);
    await submitBtn.click();
    // Wait for the create modal's title input to disappear (modal closed)
    await titleField.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Verify item does not exist
   */
  async verifyItemDoesNotExist(title: string) {
    const item = this.page.locator(
      `${this.itemRow}:has-text("${title}"), ${this.itemCard}:has-text("${title}")`
    );
    await expect(item).not.toBeVisible({ timeout: 5000 });
  }

  /**
   * Get item count
   */
  async getItemCount(): Promise<number> {
    const items = await this.page.locator(`${this.itemRow}, ${this.itemCard}`).count();
    return items;
  }

  /**
   * Update item status via inline editing
   */
  async updateStatus(title: string, newStatus: string) {
    const item = await this.findItemByTitle(title);
    const statusCell = item.locator('[data-field="status"], .status');
    await statusCell.click();

    // Select new status
    await this.page.selectOption('select, [role="combobox"]', newStatus);

    // Confirm the cell reflects the new value
    await expect(statusCell).toContainText(newStatus, { timeout: 10000 });
  }

  /**
   * Create child item
   */
  async createChildItem(
    parentTitle: string,
    childData: {
      title: string;
      description?: string;
      status?: string;
      priority?: string;
    }
  ) {
    // Navigate to parent item
    await this.clickItem(parentTitle);

    // Click create child button
    await this.page.click('button:has-text("Create Child"), button:has-text("Add Child")');

    // Fill form
    await this.fillForm(childData);
    await this.clickSave();
    // clickSave() already waits for the create modal to detach.
  }

  /**
   * Verify item hierarchy
   */
  async verifyItemIsChildOf(childTitle: string, parentTitle: string) {
    const child = await this.findItemByTitle(childTitle);
    const parentInfo = child.locator('.parent, [data-field="parent"]');
    await expect(parentInfo).toContainText(parentTitle);
  }
}
