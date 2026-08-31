import { expect, test } from '../fixtures/context-path';
import { FRESH_SETUP_ADMIN } from '../fixtures/fresh-setup';
import { LoginPage } from '../pages/login.page';

test.use({ storageState: { cookies: [], origins: [] } });

test('persists setup and selected modules across a server restart', async ({ page, request }) => {
  const statusResponse = await request.get('/api/setup/status');
  expect(statusResponse.ok()).toBeTruthy();
  expect(await statusResponse.json()).toMatchObject({
    setup_completed: true,
    admin_user_created: true,
    time_tracking_enabled: true,
    test_management_enabled: true,
  });

  await page.goto('/');
  await expect(page.locator('.setup-modal')).not.toBeVisible();
  await expect(page.locator('#emailOrUsername')).toBeVisible();

  const loginPage = new LoginPage(page);
  await loginPage.login(FRESH_SETUP_ADMIN.username, FRESH_SETUP_ADMIN.password);
  await loginPage.verifyLoginSuccess();

  await page.goto('/time');
  await expect(page.locator('#emailOrUsername')).not.toBeVisible();
  await expect(page).toHaveURL(/\/time$/);
});
