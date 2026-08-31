import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Milestone Management.
 * Covers the global (/milestones) and workspace (/workspaces/{id}/milestones)
 * list pages, and the create/edit modal embedded in Milestones.svelte.
 *
 * Form field selectors:
 *   - name:        #milestone-name  (native input)
 *   - target_date: #milestone-target-date  (native date input)
 *   - description: #milestone-description  (textarea component)
 *   - status:      #milestone-status-picker  (BasePicker trigger)
 * Category is optional and skipped for test simplicity.
 */
export class MilestonePage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly modal = 'div[role="dialog"]';
  readonly nameInput = '#milestone-name';
  readonly targetDateInput = '#milestone-target-date';
  readonly descriptionInput = '#milestone-description';
  readonly statusPicker = '#milestone-status-picker';

  async gotoGlobal() {
    await this.page.goto('/milestones');
    await this.page.waitForLoadState('networkidle');
  }

  async gotoWorkspace(workspaceId: string | number) {
    await this.page.goto(`/workspaces/${workspaceId}/milestones`);
    await this.page.waitForLoadState('networkidle');
  }

  async clickCreate() {
    const createBtn = this.page.locator('[data-testid="milestone-create-button"]').first();
    await createBtn.waitFor({ state: 'visible', timeout: 5000 });
    await createBtn.click();
    await this.page.locator(this.nameInput).waitFor({ state: 'visible', timeout: 5000 });
  }

  async fillForm(data: {
    name: string;
    target_date?: string;
    description?: string;
    status?: 'planning' | 'in-progress' | 'completed' | 'cancelled';
  }) {
    await this.page.locator(this.nameInput).fill(data.name);
    if (data.target_date) {
      await this.page.locator(this.targetDateInput).fill(data.target_date);
    }
    if (data.description) {
      await this.page.locator(this.descriptionInput).fill(data.description);
    }
    if (data.status) {
      await this.selectStatus(data.status);
    }
  }

  /**
   * Select a status via the BasePicker combobox. Click the trigger to open
   * the popover, click the option tagged `data-option-value`, then wait for
   * the picker input to display the new label. That assertion doubles as a
   * settle point: it proves (a) BasePicker's bindable `value` round-tripped
   * back to `formData.status`, and (b) the `fly` popover transition finished
   * — so the subsequent `clickSave` lands on a stable confirm button.
   *
   * Avoid pressing Enter here: `Modal.svelte` maps plain Enter on any input
   * inside the dialog to the modal's submit shortcut, which would commit the
   * save before the option is committed.
   */
  private async selectStatus(status: 'planning' | 'in-progress' | 'completed' | 'cancelled') {
    const trigger = this.page.locator(this.statusPicker);
    await trigger.click();
    const option = this.page.locator(`[data-option-value="${status}"]`).first();
    await option.waitFor({ state: 'visible', timeout: 5000 });
    await option.click();
    await expect(trigger).toHaveValue(this.statusLabel(status), { timeout: 5000 });
  }

  async clickSave() {
    const dialog = this.page.locator(this.modal);
    await dialog.locator('[data-testid="dialog-confirm"]').click();
    await this.page.locator(this.nameInput).waitFor({ state: 'detached', timeout: 10000 });
  }

  async createMilestone(data: {
    name: string;
    target_date?: string;
    description?: string;
    status?: 'planning' | 'in-progress' | 'completed' | 'cancelled';
  }) {
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();
    await this.verifyMilestoneExists(data.name);
  }

  findRowByName(name: string) {
    return this.page.locator('tbody tr').filter({ hasText: name }).first();
  }

  async verifyMilestoneExists(name: string) {
    await expect(this.findRowByName(name)).toBeVisible({ timeout: 10000 });
  }

  async verifyMilestoneDoesNotExist(name: string) {
    await expect(this.findRowByName(name)).not.toBeVisible({ timeout: 5000 });
  }

  async verifyStatus(name: string, statusLabel: string) {
    await expect(this.findRowByName(name)).toContainText(statusLabel, { timeout: 5000 });
  }

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

  async openEdit(name: string) {
    await this.openRowDropdown(name);
    await this.clickMenuItem(/edit/i);
    await this.page.locator(this.nameInput).waitFor({ state: 'visible', timeout: 5000 });
  }

  async changeStatusViaEdit(
    name: string,
    newStatus: 'planning' | 'in-progress' | 'completed' | 'cancelled'
  ) {
    await this.openEdit(name);
    await this.selectStatus(newStatus);
    await this.clickSave();
  }

  async deleteMilestone(name: string) {
    await this.openRowDropdown(name);
    await this.clickMenuItem(/delete/i);
    await this.page.locator('[data-testid="dialog-confirm"]').click();
    await this.verifyMilestoneDoesNotExist(name);
  }

  private statusLabel(status: 'planning' | 'in-progress' | 'completed' | 'cancelled'): string {
    return {
      planning: 'Planning',
      'in-progress': 'In Progress',
      completed: 'Completed',
      cancelled: 'Cancelled',
    }[status];
  }
}
