import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for User Management (Admin)
 */
export class UserPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  // Selectors
  readonly userModal = 'div[role="dialog"]';
  readonly emailInput = '#email';
  readonly usernameInput = '#username';
  readonly firstNameInput = '#first_name';
  readonly lastNameInput = '#last_name';
  readonly passwordInput = '#password';
  readonly userRow = 'tbody tr';
  readonly createUserButton = 'button:has-text("Add User")';

  /**
   * Navigate to admin users page
   */
  async goto() {
    await this.page.goto('/admin/users');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Click create user button. Waits for Modal.svelte's 100ms autofocus
   * timer to settle (it programmatically focuses the first input); without
   * this wait, focus can shift onto #first_name mid-fillForm and password
   * text ends up appended there instead of in #password.
   */
  async clickCreate() {
    await this.page.click(this.createUserButton);
    await this.page.waitForSelector(this.userModal, { timeout: 5000 });
    const dialog = this.page.locator(this.userModal);
    await expect(dialog.locator(this.firstNameInput)).toBeFocused({ timeout: 2000 });
  }

  /**
   * Fill user form. Scoped to the dialog so a stray modal elsewhere on the
   * page can't intercept the fills.
   */
  async fillForm(data: {
    email: string;
    username: string;
    firstName: string;
    lastName: string;
    password?: string;
    active?: boolean;
  }) {
    const dialog = this.page.locator(this.userModal);
    await dialog.locator(this.firstNameInput).fill(data.firstName);
    await dialog.locator(this.lastNameInput).fill(data.lastName);
    await dialog.locator(this.emailInput).fill(data.email);
    await dialog.locator(this.usernameInput).fill(data.username);

    if (data.password) {
      await dialog.locator(this.passwordInput).fill(data.password);
    }

    if (data.active) {
      // The Active toggle is a visually-hidden input inside a Checkbox label;
      // click the label wrapping it rather than the sr-only input.
      const activeRow = dialog.locator('[data-testid="create-user-active-row"]');
      await activeRow.locator('label').click();
    }
  }

  /**
   * Click save button (scoped to dialog)
   */
  async clickSave() {
    const dialog = this.page.locator(this.userModal);
    await dialog.locator('button:has-text("Create"), button:has-text("Update")').last().click();
    // Note: does NOT wait for the modal to close — `clickSaveAndWaitForClose`
    // owns the post-click polling + rate-limit retry, and other callers add
    // their own post-conditions.
  }

  /**
   * Click save and wait for the modal to close. Admin `POST /users` is behind
   * `AuthRateLimiter` (20 req/min, burst 30). When the bucket is exhausted the
   * backend returns 429 and the modal stays open with a "Too many requests"
   * toast. Detect that case and retry after a short backoff so test suites
   * that create many users in succession don't flake.
   */
  async clickSaveAndWaitForClose() {
    for (let attempt = 0; attempt < 4; attempt++) {
      await this.clickSave();
      try {
        await this.page.locator(this.userModal).waitFor({ state: 'detached', timeout: 3000 });
        return;
      } catch {
        const rateLimited = await this.page
          .getByText(/too many requests/i)
          .first()
          .isVisible()
          .catch(() => false);
        if (!rateLimited) {
          // Not a rate-limit case — let a final strict wait surface the real error
          break;
        }
        // Intentional backoff before retrying the rate-limited admin endpoint;
        // not a race wait — keep waitForTimeout here.
        await this.page.waitForTimeout(4000 + attempt * 1500);
      }
    }
    await this.page.locator(this.userModal).waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Create a new user
   */
  async createUser(data: {
    email: string;
    username: string;
    firstName: string;
    lastName: string;
    password: string;
    active?: boolean;
  }) {
    await this.goto();
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSaveAndWaitForClose();
  }

  /**
   * Find user by username
   */
  findUserByUsername(username: string) {
    return this.page.locator(`${this.userRow}:has-text("${username}")`).first();
  }

  /**
   * Find user by email
   */
  findUserByEmail(email: string) {
    return this.page.locator(`${this.userRow}:has-text("${email}")`).first();
  }

  /**
   * Verify user exists
   */
  async verifyUserExists(username: string) {
    await this.goto();
    const user = this.findUserByUsername(username);
    await expect(user).toBeVisible({ timeout: 10000 });
  }

  /**
   * Open dropdown menu for a user row
   */
  private async openRowDropdown(username: string) {
    const row = this.findUserByUsername(username);
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
   * Edit a user
   */
  async editUser(
    username: string,
    newData: {
      email?: string;
      firstName?: string;
      lastName?: string;
    }
  ) {
    await this.goto();

    // Open dropdown and click Edit
    await this.openRowDropdown(username);
    await this.clickMenuItem('Edit');

    // Wait for modal AND its autofocus timer (see clickCreate for details).
    const dialog = this.page.locator(this.userModal);
    await dialog.waitFor({ state: 'visible', timeout: 5000 });
    await expect(dialog.locator(this.firstNameInput)).toBeFocused({ timeout: 2000 });

    // Update fields
    if (newData.email) {
      await dialog.locator(this.emailInput).fill(newData.email);
    }
    if (newData.firstName) {
      await dialog.locator(this.firstNameInput).fill(newData.firstName);
    }
    if (newData.lastName) {
      await dialog.locator(this.lastNameInput).fill(newData.lastName);
    }

    await this.clickSave();

    // Wait for the edit modal to close
    await this.page.locator(this.userModal).waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Deactivate a user via dropdown menu
   */
  async deactivateUser(username: string) {
    await this.goto();
    await this.openRowDropdown(username);
    await this.clickMenuItem('Disable');

    // Confirm in dialog
    const dialog = this.page.locator(this.userModal);
    const confirmButton = dialog
      .locator('button:has-text("Confirm"), button:has-text("Yes"), button:has-text("Deactivate")')
      .last();
    await confirmButton.click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Activate a user via dropdown menu
   */
  async activateUser(username: string) {
    await this.goto();
    await this.openRowDropdown(username);
    await this.clickMenuItem('Enable');

    // Confirm in dialog
    const dialog = this.page.locator(this.userModal);
    const confirmButton = dialog
      .locator('button:has-text("Confirm"), button:has-text("Yes"), button:has-text("Activate")')
      .last();
    await confirmButton.click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  /**
   * Verify user is active. Anchored so it does NOT also match "Inactive".
   */
  async verifyUserIsActive(username: string) {
    await this.goto();
    const user = this.findUserByUsername(username);
    await expect(user.getByText('Active', { exact: true })).toBeVisible();
  }

  /**
   * Verify user is inactive
   */
  async verifyUserIsInactive(username: string) {
    await this.goto();
    const user = this.findUserByUsername(username);
    await expect(user.getByText('Inactive', { exact: true })).toBeVisible();
  }

  /**
   * Get user count
   */
  async getUserCount(): Promise<number> {
    const users = this.page.locator(this.userRow);
    await expect(users.first()).toBeVisible({ timeout: 10000 });
    return users.count();
  }

  /**
   * Search for user
   */
  async searchUser(query: string) {
    const searchInput = this.page.locator('input[placeholder*="Search users"]').first();
    await searchInput.fill(query);
    // Debounced search fires an XHR; wait for the network to settle.
    await this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }
}
