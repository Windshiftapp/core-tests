import { expect, test } from '../fixtures/context-path';
import { AdminPage } from '../pages/admin.page';

/**
 * Admin Settings Tests
 * Tests admin panel navigation, tab content, and permission guards
 */

test.describe('Admin Settings', () => {
  let adminPage: AdminPage;

  test.beforeEach(async ({ page }) => {
    adminPage = new AdminPage(page);
  });

  test.describe('Admin Panel Access', () => {
    test('should access admin panel when authenticated as admin', async () => {
      await adminPage.verifyAdminAccessible();
    });

    test('should display admin content', async () => {
      await adminPage.goto();

      // Should see admin search input as sentinel
      const searchInput = adminPage.page.locator('#admin-search');
      await expect(searchInput).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe('Admin Panel Navigation', () => {
    // Sentinels unique to each tab's content — proves the destination view
    // actually rendered, not just that some <main> is on screen.
    const usersSentinel = (adminPage: AdminPage) =>
      adminPage.page.getByRole('button', { name: 'Add User' });
    // "Security" is the admin settings tab (there is no /admin/settings route).
    const securitySentinel = (adminPage: AdminPage) =>
      adminPage.page.getByText('Calendar Feed Subscriptions');

    test('should navigate to Users tab', async () => {
      await adminPage.goto();
      await adminPage.clickTab('Users');
      await adminPage.expectOnTab('users');
      await expect(usersSentinel(adminPage)).toBeVisible({ timeout: 5000 });
    });

    test('should navigate to Security (settings) tab', async () => {
      await adminPage.goto();
      await adminPage.clickTab('Security');
      await adminPage.expectOnTab('security');
      await expect(securitySentinel(adminPage)).toBeVisible({ timeout: 5000 });
    });

    test('should navigate between tabs', async () => {
      await adminPage.goto();

      await adminPage.clickTab('Users');
      await adminPage.expectOnTab('users');
      await expect(usersSentinel(adminPage)).toBeVisible({ timeout: 5000 });

      await adminPage.clickTab('Security');
      await adminPage.expectOnTab('security');
      await expect(securitySentinel(adminPage)).toBeVisible({ timeout: 5000 });
    });

    test('should display tab content when switching', async () => {
      await adminPage.goto();

      const tabs = await adminPage.getVisibleTabs();
      expect(tabs.length).toBeGreaterThan(0);

      // Click through each visible tab. clickTab() now asserts the URL actually
      // changed to that tab's route, so a tab that fails to navigate fails the
      // test instead of being papered over by a generic <main> check.
      for (const tab of tabs) {
        if (tab) {
          await adminPage.clickTab(tab);
          await adminPage.verifyTabContentVisible();
        }
      }
    });

    test('keeps the centralized service user grant form within its panel when zoomed', async ({
      page,
    }) => {
      await page.setViewportSize({ width: 768, height: 900 });
      await page.goto('/admin/security');

      const addGrant = page.getByTestId('agent-security-add-grant');
      await expect(addGrant).toBeVisible({ timeout: 10000 });

      await page.evaluate(() => {
        document.documentElement.style.zoom = '200%';
      });

      await expect
        .poll(() => addGrant.evaluate((element) => element.scrollWidth <= element.clientWidth))
        .toBe(true);
    });

    test('keeps direct admin navigation usable on a narrow screen', async ({ page }) => {
      const pageOverflow = () =>
        page.evaluate(() => {
          const viewportWidth = document.documentElement.clientWidth;
          if (document.documentElement.scrollWidth <= viewportWidth) return null;

          const offenders = Array.from(document.body.querySelectorAll('*'))
            .map((element) => ({ element, bounds: element.getBoundingClientRect() }))
            .filter(({ bounds }) => bounds.right > viewportWidth + 1)
            .slice(0, 8)
            .map(({ element, bounds }) => ({
              element: element.tagName.toLowerCase(),
              className: element.className?.toString().slice(0, 120) || '',
              testId: element.getAttribute('data-testid'),
              right: Math.round(bounds.right),
            }));

          return {
            viewportWidth,
            pageWidth: document.documentElement.scrollWidth,
            offenders,
          };
        });

      await page.setViewportSize({ width: 320, height: 720 });
      await page.goto('/admin/security');

      const navigationToggle = page.getByTestId('admin-navigation-toggle');
      const navigation = page.locator('#admin-navigation');
      await expect(navigationToggle).toBeVisible({ timeout: 10000 });
      await expect(page.getByTestId('admin-active-section')).toHaveText('Security');
      await expect(navigation).toBeHidden();
      await expect.poll(pageOverflow).toBeNull();

      await navigationToggle.click();
      await expect(navigation).toBeVisible();

      await page.locator('#admin-navigation-item-users').click();
      await expect(page).toHaveURL(/\/admin\/users(?:[/?#]|$)/);
      await expect(navigation).toBeHidden();
      await expect.poll(pageOverflow).toBeNull();
    });
  });

  test.describe('Permission Guard', () => {
    test('should not allow unauthenticated access to admin', async ({ page, context }) => {
      // Clear cookies to simulate unauthenticated user
      await context.clearCookies();

      await page.goto('/admin');
      // The app should bounce us back to the login screen
      const loginInput = page.locator('#emailOrUsername');
      await expect(loginInput).toBeVisible({ timeout: 10000 });
    });
  });
});
