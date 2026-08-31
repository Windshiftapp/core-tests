import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Time Tracking
 */
export class TimeTrackingPage {
  constructor(private page: Page) {}

  /**
   * Navigate to time entry page
   */
  async goto() {
    await this.page.goto('/time');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Navigate to time projects page
   */
  async gotoProjects() {
    await this.page.goto('/time/projects');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Create a new time project via UI
   */
  async createProject(data: { name: string; description?: string; customer?: string }) {
    await this.gotoProjects();

    // Click "Add Project" button
    await this.page.getByRole('button', { name: /Add Project/i }).click();
    await this.page.waitForSelector('div[role="dialog"]', { timeout: 5000 });

    const dialog = this.page.locator('div[role="dialog"]');

    // Fill project name
    await this.page.fill('#time-project-name', data.name);

    // Select customer (required) — the customer dropdown is the 3rd item in the 2-col grid
    if (data.customer) {
      const customerSection = dialog.locator('.grid > div').nth(2);
      await customerSection.locator('button').click();
      const customerOption = this.page
        .locator('[role="option"]')
        .filter({ hasText: data.customer });
      await customerOption.first().waitFor({ state: 'visible', timeout: 5000 });
      await customerOption.first().click();
      await customerOption.first().waitFor({ state: 'detached', timeout: 5000 });
    }

    if (data.description) {
      await this.page.fill('#time-project-description', data.description);
    }

    // Click the save/create button in dialog footer and wait for the modal to close
    await dialog.getByRole('button', { name: /Create/i }).click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Find project by name in the table
   */
  findProjectByName(name: string) {
    return this.page.locator(`tbody tr:has-text("${name}")`).first();
  }

  /**
   * Verify project exists in the projects list
   */
  async verifyProjectExists(name: string) {
    await this.gotoProjects();
    const project = this.findProjectByName(name);
    await expect(project).toBeVisible({ timeout: 10000 });
  }

  /**
   * Delete a project via the row action dropdown
   */
  async editProject(
    currentName: string,
    newData: { name?: string; description?: string; status?: string }
  ) {
    await this.gotoProjects();
    const row = this.findProjectByName(currentName);

    await row.locator('button').last().click();
    await this.page
      .getByRole('menuitem', { name: /Edit/i })
      .waitFor({ state: 'visible', timeout: 5000 });
    await this.page.getByRole('menuitem', { name: /Edit/i }).click();
    await this.page.waitForSelector('div[role="dialog"]', { timeout: 5000 });

    const dialog = this.page.locator('div[role="dialog"]');
    if (newData.name) {
      await this.page.fill('#time-project-name', newData.name);
    }
    if (newData.status) {
      await dialog.locator('.grid > div').nth(1).locator('button').click();
      const statusOption = this.page.locator('[role="option"]').filter({ hasText: newData.status });
      await statusOption.first().waitFor({ state: 'visible', timeout: 5000 });
      await statusOption.first().click();
      await statusOption.first().waitFor({ state: 'detached', timeout: 5000 });
    }
    if (newData.description !== undefined) {
      await this.page.fill('#time-project-description', newData.description);
    }

    await dialog.getByRole('button', { name: /Update Project|Save/i }).click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Delete a project via the row action dropdown
   */
  async deleteProject(name: string) {
    await this.gotoProjects();
    const row = this.findProjectByName(name);

    // Click the action dropdown (three-dot button) on the row
    await row.locator('button').last().click();
    await this.page
      .getByRole('menuitem', { name: /Delete/i })
      .waitFor({ state: 'visible', timeout: 5000 });

    // Click "Delete" menu item and wait for the confirmation dialog
    await this.page.getByRole('menuitem', { name: /Delete/i }).click();
    const confirmDialog = this.page.locator('div[role="dialog"]').last();
    await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });

    // Confirm deletion and wait for the project row to disappear
    await confirmDialog.getByRole('button', { name: /Delete/i }).click();
    await row.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Log time via the time entry modal
   */
  async logTime(data: { project: string; description: string; duration: string; date?: string }) {
    // Click "Log Time" button
    await this.page.getByTestId('time-log-open').click();
    const dialog = this.page.getByTestId('time-log-modal');
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Select the project through its stable picker option. Filtering by the
    // unique project name leaves one result without coupling the test to text.
    const projectCombobox = dialog.locator('#time-log-project');
    await projectCombobox.click();
    await projectCombobox.fill(data.project);
    const projectOption = this.page.getByTestId(/^time-log-project-option-/);
    await expect(projectOption).toHaveCount(1);
    await projectOption.click();
    await expect(this.page.getByTestId('picker-dropdown')).toBeHidden();
    await expect(projectCombobox).toHaveValue(data.project, { timeout: 10000 });

    // Fill description
    const description = dialog.locator('#time-log-description');
    await description.fill(data.description);
    await expect(description).toHaveValue(data.description);

    // Fill duration
    const duration = dialog.locator('#time-log-duration');
    await duration.fill(data.duration);
    await expect(duration).toHaveValue(data.duration);

    if (data.date) {
      await dialog.locator('#time-log-date').fill(data.date);
    }

    // Click confirm button in dialog footer and wait for the dialog to close
    const confirmButton = dialog.getByTestId('dialog-confirm');
    await expect(confirmButton).toBeEnabled({ timeout: 20000 });
    await confirmButton.click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Find worklog by description in the table
   */
  findWorklogByDescription(description: string) {
    return this.page.locator(`tbody tr:has-text("${description}")`).first();
  }

  /**
   * Verify worklog exists in the time entry list
   */
  async verifyWorklogExists(description: string) {
    const worklog = this.findWorklogByDescription(description);
    await expect(worklog).toBeVisible({ timeout: 10000 });
  }
}
