import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Iteration Management.
 * Covers the global (/iterations) and workspace (/workspaces/{id}/iterations)
 * list pages, the IterationModal (used for both create and edit), and row-
 * level actions (dropdown menu → edit / delete).
 */
export class IterationPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  // --- Selectors ---
  readonly modal = 'div[role="dialog"]';
  readonly nameInput = '#iteration-name-input';
  readonly typeSelect = '#iteration-type-select';
  readonly startDateInput = '#iteration-start-date-input';
  readonly endDateInput = '#iteration-end-date-input';
  readonly statusSelect = '#iteration-status-select';

  // --- Navigation ---
  async gotoGlobal() {
    await this.page.goto('/iterations');
    await this.page.waitForLoadState('networkidle');
  }

  async gotoWorkspace(workspaceId: string | number) {
    await this.page.goto(`/workspaces/${workspaceId}/iterations`);
    await this.page.waitForLoadState('networkidle');
  }

  // --- Modal helpers ---

  /**
   * Click the "Create Iteration" button and wait for the modal to open.
   * Both the PageHeader action and the EmptyState action render with the
   * same `data-testid`, so `.first()` reliably picks the visible one.
   */
  async clickCreate() {
    const createBtn = this.page.locator('[data-testid="iteration-create-button"]').first();
    await createBtn.waitFor({ state: 'visible', timeout: 5000 });
    await createBtn.click();
    await this.page.locator(this.nameInput).waitFor({ state: 'visible', timeout: 5000 });
  }

  /**
   * Fill the iteration modal. Dates are ISO `YYYY-MM-DD`.
   * Type is picked by name via the melt-ui Select trigger.
   */
  async fillForm(data: {
    name: string;
    start_date: string;
    end_date: string;
    status?: 'planned' | 'active' | 'completed' | 'cancelled';
    type_name?: string;
  }) {
    await this.page.locator(this.nameInput).fill(data.name);
    await this.page.locator(this.startDateInput).fill(data.start_date);
    await this.page.locator(this.endDateInput).fill(data.end_date);
    if (data.type_name) {
      await this.selectOption(this.typeSelect, data.type_name);
    } else {
      // Default: click the first option whose text is NOT the "Select type..."
      // placeholder. Select.svelte doesn't expose `aria-disabled`, so we filter
      // by the visible label instead.
      await this.page.locator(this.typeSelect).click();
      const option = this.page
        .locator('[role="option"]')
        .filter({ hasNotText: /select type/i })
        .first();
      await option.waitFor({ state: 'visible', timeout: 5000 });
      await option.click();
    }
    if (data.status) {
      await this.selectOption(this.statusSelect, this.statusLabel(data.status));
    }
  }

  async clickSave() {
    const dialog = this.page.locator(this.modal);
    await dialog.locator('[data-testid="dialog-confirm"]').click();
    await this.page.locator(this.nameInput).waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Full create flow: click Create → fill → save → verify the row appears.
   */
  async createIteration(data: {
    name: string;
    start_date: string;
    end_date: string;
    status?: 'planned' | 'active' | 'completed' | 'cancelled';
    type_name?: string;
  }) {
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();
    await this.verifyIterationExists(data.name);
  }

  // --- Row helpers ---

  /**
   * Match an iteration row in the DataTable by name text. DataTable renders
   * rows as `<tr>` — scoping to tbody keeps us off the header row.
   */
  findRowByName(name: string) {
    return this.page.locator('tbody tr').filter({ hasText: name }).first();
  }

  async verifyIterationExists(name: string) {
    await expect(this.findRowByName(name)).toBeVisible({ timeout: 10000 });
  }

  async verifyIterationDoesNotExist(name: string) {
    await expect(this.findRowByName(name)).not.toBeVisible({ timeout: 5000 });
  }

  async verifyStatus(name: string, statusLabel: string) {
    await expect(this.findRowByName(name)).toContainText(statusLabel, { timeout: 5000 });
  }

  /**
   * Open the actions dropdown on a row and click a menu item by text.
   */
  private async openRowDropdown(name: string) {
    const row = this.findRowByName(name);
    await row.locator('button').last().click();
    await this.page
      .locator('button[role="menuitem"]')
      .first()
      .waitFor({ state: 'visible', timeout: 5000 });
  }

  private async clickMenuItem(text: RegExp | string) {
    await this.page
      .locator('button[role="menuitem"]')
      .filter({ hasText: typeof text === 'string' ? new RegExp(text, 'i') : text })
      .first()
      .click();
  }

  /**
   * Open the edit modal from the row dropdown. After this returns, the name
   * input is visible and prefilled.
   */
  async openEdit(name: string) {
    await this.openRowDropdown(name);
    await this.clickMenuItem(/edit/i);
    await this.page.locator(this.nameInput).waitFor({ state: 'visible', timeout: 5000 });
  }

  /**
   * Open the edit modal, change the status select, and save.
   */
  async changeStatusViaEdit(
    name: string,
    newStatus: 'planned' | 'active' | 'completed' | 'cancelled'
  ) {
    await this.openEdit(name);
    await this.selectOption(this.statusSelect, this.statusLabel(newStatus));
    await this.clickSave();
  }

  /**
   * Delete via the row dropdown. Uses the global `useConfirm` modal, whose
   * confirm button exposes `data-testid="dialog-confirm"`.
   */
  async deleteIteration(name: string) {
    await this.openRowDropdown(name);
    await this.clickMenuItem(/delete/i);
    await this.page.locator('[data-testid="dialog-confirm"]').click();
    await this.verifyIterationDoesNotExist(name);
  }

  // --- Internal helpers ---

  /**
   * Click a melt-ui Select trigger by id, then click the listbox option whose
   * text matches `optionText`. The option labels (Planned, Active, Completed,
   * Cancelled, Sprint) don't share substrings, so a case-insensitive contains
   * match is safe — strict anchors break because the option element has a
   * trailing `<img>` that pads the textContent with whitespace.
   */
  private async selectOption(triggerId: string, optionText: string) {
    await this.page.locator(triggerId).click();
    const option = this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(this.escapeRegex(optionText), 'i') })
      .first();
    await option.waitFor({ state: 'visible', timeout: 5000 });
    await option.click();
  }

  private statusLabel(status: 'planned' | 'active' | 'completed' | 'cancelled'): string {
    // Matches the English translations in sprints.status* keys.
    return { planned: 'Planned', active: 'Active', completed: 'Completed', cancelled: 'Cancelled' }[
      status
    ];
  }

  private escapeRegex(s: string): string {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
}
