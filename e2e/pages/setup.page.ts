import { expect, type Page, type Response } from '../fixtures/context-path';

/** Browser interactions for the two-step first-install assistant. */
export class SetupPage {
  constructor(private page: Page) {}

  readonly setupModal = this.page.locator('.setup-modal');
  readonly adminHeading = this.page.getByRole('heading', {
    name: 'Create Administrator Account',
  });
  readonly modulesHeading = this.page.getByRole('heading', {
    name: 'Configure Modules',
  });
  readonly validationError = this.setupModal.getByText(/required|passwords do not match/i);
  readonly testManagementToggle = this.setupModal.getByRole('switch');

  async goto() {
    await this.page.goto('/');
    await this.adminHeading.waitFor({ state: 'visible', timeout: 10000 });
  }

  async verifySetupVisible() {
    await expect(this.setupModal).toBeVisible();
    await expect(this.adminHeading).toBeVisible();
  }

  async fillAdminUserForm(data: {
    email: string;
    username: string;
    password: string;
    firstName: string;
    lastName: string;
  }) {
    const email = this.page.locator('#email');
    await email.click();
    await email.pressSequentially(data.email);
    await expect(email).toHaveValue(data.email);
    await this.page.locator('#username').fill(data.username);
    await this.page.locator('#password').fill(data.password);
    await this.page.locator('#confirm_password').fill(data.password);
    await this.page.locator('#first_name').fill(data.firstName);
    await this.page.locator('#last_name').fill(data.lastName);
  }

  async continueToModules() {
    await this.setupModal.getByRole('button', { name: /^Next/ }).click();
  }

  async setTestManagement(enabled: boolean) {
    const checked = (await this.testManagementToggle.getAttribute('aria-checked')) === 'true';
    if (checked !== enabled) await this.testManagementToggle.click();
  }

  async completeSetup(): Promise<Response> {
    const responsePromise = this.page.waitForResponse((response) =>
      response.url().includes('/api/setup/complete')
    );
    const reloadPromise = this.page.waitForEvent(
      'framenavigated',
      (frame) => frame === this.page.mainFrame()
    );
    await this.setupModal.getByRole('button', { name: /^Complete Setup/ }).click();
    const response = await responsePromise;
    await reloadPromise;
    await this.page.waitForLoadState('domcontentloaded');
    return response;
  }

  async verifySetupCompleted() {
    await expect(this.setupModal).not.toBeVisible({ timeout: 15000 });
  }
}
