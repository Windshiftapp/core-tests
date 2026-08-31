import { expect, test } from '../fixtures/context-path';
import { generateUser } from '../fixtures/test-data';
import { UserPage } from '../pages/user.page';

/**
 * User Management Tests
 * Tests user CRUD operations
 * Requires admin permissions
 */

test.describe('User Management', () => {
  let userPage: UserPage;
  let testUser: ReturnType<typeof generateUser>;

  test.beforeEach(async ({ page }) => {
    userPage = new UserPage(page);
    testUser = generateUser();
  });

  test.describe('Create User', () => {
    test('should create user with valid data', async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });

      // Verify user was created
      await userPage.verifyUserExists(testUser.username);
    });

    test('should create user as inactive by default', async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });

      // The Active toggle in the create dialog defaults to off so the admin
      // sees the resulting state up-front rather than being surprised later.
      await userPage.verifyUserIsInactive(testUser.username);
    });

    test('should create active user when Active toggle is checked', async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
        active: true,
      });

      await userPage.verifyUserIsActive(testUser.username);
    });

    test('should validate unique email', async () => {
      // Create first user
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });

      // Try to create another with same email
      const duplicateUser = generateUser('duplicate');
      await userPage.goto();
      await userPage.clickCreate();
      await userPage.fillForm({
        email: testUser.email, // Same email
        username: duplicateUser.username, // Different username
        firstName: duplicateUser.first_name,
        lastName: duplicateUser.last_name,
        password: duplicateUser.password_hash,
      });
      // The server rejects the duplicate with a 4xx; wait for that response
      // and confirm the modal stays up carrying the error.
      const dupeResponse = userPage.page.waitForResponse(
        (res) => res.request().method() === 'POST' && res.url().includes('/api/users'),
        { timeout: 10000 }
      );
      await userPage.clickSave();
      const resp = await dupeResponse;
      expect([400, 409, 422], `expected 4xx for duplicate email, got ${resp.status()}`).toContain(
        resp.status()
      );
      await expect(userPage.page.locator(userPage.userModal)).toBeVisible();
      await expect(
        userPage.page.locator('[data-testid="toast"][data-toast-variant="error"]').first()
      ).toBeVisible({ timeout: 5000 });
    });

    test('should validate unique username', async () => {
      // Create first user
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });

      // Try to create another with same username
      const duplicateUser = generateUser('duplicate');
      await userPage.goto();
      await userPage.clickCreate();
      await userPage.fillForm({
        email: duplicateUser.email, // Different email
        username: testUser.username, // Same username
        firstName: duplicateUser.first_name,
        lastName: duplicateUser.last_name,
        password: duplicateUser.password_hash,
      });
      const dupeResponse = userPage.page.waitForResponse(
        (res) => res.request().method() === 'POST' && res.url().includes('/api/users'),
        { timeout: 10000 }
      );
      await userPage.clickSave();
      const resp = await dupeResponse;
      expect(
        [400, 409, 422],
        `expected 4xx for duplicate username, got ${resp.status()}`
      ).toContain(resp.status());
      await expect(userPage.page.locator(userPage.userModal)).toBeVisible();
      await expect(
        userPage.page.locator('[data-testid="toast"][data-toast-variant="error"]').first()
      ).toBeVisible({ timeout: 5000 });
    });

    test('should require email', async ({ page }) => {
      await userPage.goto();
      await userPage.clickCreate();

      // Fill all except email
      await page.fill('#username', testUser.username);
      await page.fill('#first_name', testUser.first_name);
      await page.fill('#last_name', testUser.last_name);
      await page.fill('#password', testUser.password_hash);

      await userPage.clickSave();

      // Client-side validation keeps the modal open — assert directly.
      const modal = page.locator(userPage.userModal);
      await expect(modal).toBeVisible();
    });

    test('should require password for new user', async ({ page }) => {
      await userPage.goto();
      await userPage.clickCreate();

      // Fill all except password
      await page.fill('#email', testUser.email);
      await page.fill('#username', testUser.username);
      await page.fill('#first_name', testUser.first_name);
      await page.fill('#last_name', testUser.last_name);

      await userPage.clickSave();

      // Client-side validation keeps the modal open — assert directly.
      const modal = page.locator(userPage.userModal);
      await expect(modal).toBeVisible();
    });
  });

  test.describe('View User', () => {
    test.beforeEach(async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });
    });

    test('should display user in list', async () => {
      await userPage.goto();

      // Find user
      const user = userPage.findUserByUsername(testUser.username);
      await expect(user).toBeVisible();

      // Verify details
      await expect(user).toContainText(testUser.email);
    });

    test('should search users', async () => {
      // Seed a second user with a distinct username so the search has a
      // non-match to filter out — otherwise "search" can't be distinguished
      // from "list everything".
      const otherUser = generateUser('search-other');
      await userPage.createUser({
        email: otherUser.email,
        username: otherUser.username,
        firstName: otherUser.first_name,
        lastName: otherUser.last_name,
        password: otherUser.password_hash,
      });

      await userPage.goto();
      await userPage.searchUser(testUser.username);

      // The matching user stays; the non-matching user is filtered out.
      await expect(userPage.findUserByUsername(testUser.username)).toBeVisible({ timeout: 10000 });
      await expect(
        userPage.page.locator(`${userPage.userRow}:has-text("${otherUser.username}")`)
      ).toHaveCount(0);
    });
  });

  test.describe('Edit User', () => {
    test.beforeEach(async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });
    });

    test('should update user email', async () => {
      const newEmail = `updated.${testUser.email}`;

      await userPage.editUser(testUser.username, {
        email: newEmail,
      });

      // Verify update
      await userPage.goto();
      const user = userPage.findUserByUsername(testUser.username);
      await expect(user).toContainText(newEmail);
    });

    test('should update user name', async () => {
      await userPage.editUser(testUser.username, {
        firstName: 'Updated',
        lastName: 'Name',
      });

      // Verify update
      await userPage.goto();
      const user = userPage.findUserByUsername(testUser.username);
      await expect(user).toContainText('Updated');
      await expect(user).toContainText('Name');
    });
  });

  test.describe('User Status', () => {
    test.beforeEach(async () => {
      await userPage.createUser({
        email: testUser.email,
        username: testUser.username,
        firstName: testUser.first_name,
        lastName: testUser.last_name,
        password: testUser.password_hash,
      });
      // Since core commit 73b2b39 (auth hardening) users are created inactive
      // by default — explicit activation is required before they can be
      // disabled/re-activated through the UI flow these tests exercise.
      await userPage.activateUser(testUser.username);
    });

    test('should deactivate user', async () => {
      await userPage.deactivateUser(testUser.username);

      // Verify user is inactive
      await userPage.verifyUserIsInactive(testUser.username);
    });

    test('should activate user', async () => {
      // First deactivate
      await userPage.deactivateUser(testUser.username);

      // Then activate
      await userPage.activateUser(testUser.username);

      // Verify user is active
      await userPage.verifyUserIsActive(testUser.username);
    });
  });

  test.describe('User List', () => {
    test('should display multiple users', async () => {
      const user1 = generateUser('1');
      const user2 = generateUser('2');
      const user3 = generateUser('3');

      await userPage.createUser({
        email: user1.email,
        username: user1.username,
        firstName: user1.first_name,
        lastName: user1.last_name,
        password: user1.password_hash,
      });

      await userPage.createUser({
        email: user2.email,
        username: user2.username,
        firstName: user2.first_name,
        lastName: user2.last_name,
        password: user2.password_hash,
      });

      await userPage.createUser({
        email: user3.email,
        username: user3.username,
        firstName: user3.first_name,
        lastName: user3.last_name,
        password: user3.password_hash,
      });

      await userPage.goto();

      // Verify all are visible
      await userPage.verifyUserExists(user1.username);
      await userPage.verifyUserExists(user2.username);
      await userPage.verifyUserExists(user3.username);

      // Get count
      const count = await userPage.getUserCount();
      expect(count).toBeGreaterThanOrEqual(4); // 3 + admin
    });
  });
});
