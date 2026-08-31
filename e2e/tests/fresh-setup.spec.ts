import { expect, test } from '../fixtures/context-path';
import { FRESH_SETUP_ADMIN } from '../fixtures/fresh-setup';
import { LoginPage } from '../pages/login.page';
import { SetupPage } from '../pages/setup.page';

test.use({ storageState: { cookies: [], origins: [] } });

test('completes first-install setup entirely through the browser', async ({ page, request }) => {
  const setupPage = new SetupPage(page);
  await setupPage.goto();
  await setupPage.verifySetupVisible();

  await setupPage.continueToModules();
  await expect(setupPage.validationError).toContainText('Please fill in all required fields');
  await expect(setupPage.adminHeading).toBeVisible();

  const before = await request.get('/api/setup/status');
  expect(before.ok()).toBeTruthy();
  expect((await before.json()).setup_completed).toBe(false);

  await setupPage.fillAdminUserForm(FRESH_SETUP_ADMIN);
  await setupPage.continueToModules();
  await expect(setupPage.modulesHeading).toBeVisible();
  await setupPage.setTestManagement(true);
  await expect(setupPage.testManagementToggle).toHaveAttribute('aria-checked', 'true');

  const completion = await setupPage.completeSetup();
  expect(completion.ok(), `setup completion returned ${completion.status()}`).toBeTruthy();
  await setupPage.verifySetupCompleted();

  const after = await request.get('/api/setup/status');
  expect(after.ok()).toBeTruthy();
  expect(await after.json()).toMatchObject({
    setup_completed: true,
    admin_user_created: true,
    time_tracking_enabled: true,
    test_management_enabled: true,
  });

  const loginPage = new LoginPage(page);
  await loginPage.loginIfNeeded(FRESH_SETUP_ADMIN.username, FRESH_SETUP_ADMIN.password);
  await loginPage.verifyLoginSuccess();
});
