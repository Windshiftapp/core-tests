import { expect, type Page } from '../fixtures/context-path';

/**
 * Field type value to display label mapping (matches frontend fieldTypes array)
 */
export const FIELD_TYPE_LABELS: Record<string, string> = {
  text: 'Single Line Text',
  textarea: 'Multi Line Text',
  select: 'Single Select',
  multiselect: 'Multi Select',
  number: 'Number',
  date: 'Date',
  user: 'User',
  iteration: 'Iteration',
  milestone: 'Milestone',
  asset: 'Asset',
  portalcustomer: 'Portal Customer',
  customerorganisation: 'Customer Organisation',
  linking: 'Linking',
};

/**
 * Page Object for Custom Fields Admin UI
 */
export class CustomFieldsPage {
  constructor(private page: Page) {}

  // Selectors
  readonly createButton = '#create-field-button';
  readonly modal = '[data-testid="custom-field-dialog"]';
  readonly nameInput = '#field-name';
  readonly fieldRow = 'tbody tr';

  /**
   * Navigate to custom fields admin page
   */
  async goto() {
    await this.page.goto('/admin/custom-fields');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click the "Create Field" button
   */
  async clickCreateField() {
    await this.page.locator(this.createButton).click();
    await this.page.waitForSelector(this.modal, { timeout: 5000 });
  }

  /**
   * Fill the field name input
   */
  async fillFieldName(name: string) {
    await this.page.fill(this.nameInput, name);
  }

  /**
   * Select a field type from the dropdown menu
   */
  async selectFieldType(typeValue: string) {
    const label = FIELD_TYPE_LABELS[typeValue];
    if (!label) throw new Error(`Unknown field type: ${typeValue}`);

    const dialog = this.page.locator(this.modal);

    // Click the field type dropdown trigger button (shows current type label or "Select type...")
    const trigger = dialog.getByTestId('custom-field-type-trigger');
    await trigger.click();

    // Wait for the menu to open, pick the option, then wait for it to close.
    const menuItem = this.page.getByTestId(`custom-field-type-${typeValue}`);
    await menuItem.waitFor({ state: 'visible', timeout: 5000 });
    await menuItem.click();
    await menuItem.waitFor({ state: 'detached', timeout: 5000 });
  }

  /**
   * Fill options for select/multiselect fields using individual inputs + "Add Option" button
   */
  async fillOptions(options: string[]) {
    const dialog = this.page.locator(this.modal);

    for (let i = 0; i < options.length; i++) {
      // Find all option inputs (placeholder "Option label")
      const optionInputs = dialog.locator('input[placeholder="Option label"]');
      const currentCount = await optionInputs.count();

      if (i >= currentCount) {
        // Need to add a new option row; wait for the extra input to render
        await dialog.getByRole('button', { name: /Add Option/i }).click();
        await expect(dialog.locator('input[placeholder="Option label"]')).toHaveCount(
          currentCount + 1,
          { timeout: 5000 }
        );
      }

      // Fill the option input at this index
      const input = dialog.locator('input[placeholder="Option label"]').nth(i);
      await input.fill(options[i]);
    }
  }

  /**
   * Click the save/create button in the dialog footer
   */
  async clickSave() {
    const dialog = this.page.locator(this.modal);
    const confirmButton = dialog.getByTestId('custom-field-save');
    await confirmButton.click();
    // createField() follows up with `waitForSelector(modal, state: hidden)`, so
    // we don't need to duplicate the close wait here.
  }

  /**
   * Create a custom field through the UI
   */
  async createField(data: { name: string; type: string; options?: string[] }) {
    await this.clickCreateField();
    await this.fillFieldName(data.name);

    // Only change type if not 'text' (default)
    if (data.type !== 'text') {
      await this.selectFieldType(data.type);
    }

    if (data.options && data.options.length > 0) {
      await this.fillOptions(data.options);
    }

    await this.clickSave();

    // Wait for modal to close
    await this.page.waitForSelector(this.modal, { state: 'hidden', timeout: 10000 });
  }

  /**
   * Open an existing field's edit modal via the row action dropdown.
   * Mirrors deleteField()'s dropdown handling but picks "Edit".
   */
  async openFieldForEdit(name: string) {
    const field = this.findFieldByName(name);
    const actionButton = field.getByTestId('custom-field-actions');
    await actionButton.click();
    const editMenuItem = this.page.getByTestId('custom-field-edit');
    await editMenuItem.waitFor({ state: 'visible', timeout: 5000 });
    await editMenuItem.click();
    await this.page.waitForSelector(this.modal, { timeout: 5000 });
  }

  /**
   * Read the option label values currently rendered in the open create/edit
   * modal (inputs with placeholder "Option label"), in order.
   */
  async getOptionValues(): Promise<string[]> {
    const dialog = this.page.locator(this.modal);
    const inputs = dialog.locator('input[placeholder="Option label"]');
    await inputs.first().waitFor({ state: 'visible', timeout: 5000 });
    return inputs.evaluateAll((els) => els.map((el) => (el as HTMLInputElement).value));
  }

  /**
   * Close the open modal without saving (Escape).
   */
  async closeModal() {
    await this.page
      .locator(this.modal)
      .last()
      .press('Escape')
      .catch(() => {});
    await this.page.waitForSelector(this.modal, { state: 'hidden', timeout: 5000 }).catch(() => {});
  }

  /**
   * Find a field row by name
   */
  findFieldByName(name: string) {
    return this.page.getByTestId('custom-field-row').filter({ hasText: name }).first();
  }

  /**
   * Verify a field exists in the table
   */
  async verifyFieldExists(name: string) {
    const field = this.findFieldByName(name);
    await expect(field).toBeVisible({ timeout: 10000 });
  }

  /**
   * Verify a field has the correct type label in the table
   */
  async verifyFieldType(name: string, expectedTypeLabel: string) {
    const field = this.findFieldByName(name);
    await expect(field).toContainText(expectedTypeLabel, { timeout: 5000 });
  }

  /**
   * Verify a field does not exist in the table
   */
  async verifyFieldDoesNotExist(name: string) {
    const field = this.page.getByTestId('custom-field-row').filter({ hasText: name });
    await expect(field).not.toBeVisible({ timeout: 5000 });
  }

  /**
   * Delete a field via the row's action dropdown
   */
  async deleteField(name: string) {
    const field = this.findFieldByName(name);

    // Click the action dropdown trigger (three-dot button) on the row
    const actionButton = field.getByTestId('custom-field-actions');
    await actionButton.click();
    const deleteMenuItem = this.page.getByTestId('custom-field-delete');
    await deleteMenuItem.waitFor({ state: 'visible', timeout: 5000 });

    // Click "Delete" from the dropdown menu and wait for the confirm dialog
    await deleteMenuItem.click();
    const confirmDialog = this.page.locator('div[role="dialog"]').last();
    await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });

    // Confirm deletion and wait for the row to disappear
    await confirmDialog.getByRole('button', { name: /Delete/i }).click();
    await field.waitFor({ state: 'detached', timeout: 10000 });
  }
}
